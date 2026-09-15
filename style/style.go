package style

import (
	"maps"

	"charm.land/lipgloss/v2"
)

// Style identifies a style category for YAML highlighting.
//
// Style constants are used as keys in [Styles] maps to associate token
// categories with [lipgloss.Style] formatting. Custom keys, such as overlay
// kinds, are conversions of a string: style.Style("mine").
type Style string

// Style constants for YAML highlighting.
// Names follow Pygments token naming conventions where applicable.
const (
	// Text is a default/fallback style.
	Text Style = "text"
	// TextAccent styles accented text.
	TextAccent Style = "textAccent"
	// TextAccentDim styles dimmed accented text.
	TextAccentDim Style = "textAccentDim"
	// TextSubtle styles de-emphasized text.
	TextSubtle Style = "textSubtle"
	// TextSubtleDim styles dimmed de-emphasized text.
	TextSubtleDim Style = "textSubtleDim"
	// TextOK styles success/OK text.
	TextOK Style = "textOK"
	// TextWarn styles warning text.
	TextWarn Style = "textWarn"
	// TextError styles error text.
	TextError Style = "textError"
	// Comment styles comments (#).
	Comment Style = "comment"
	// CommentPreproc styles preprocessor comment, e.g.: %YAML, %TAG.
	CommentPreproc Style = "commentPreproc"
	// Generic is a parent style for generic tokens.
	Generic Style = "generic"
	// GenericDeleted styles lines deleted in diff (-).
	GenericDeleted Style = "genericDeleted"
	// GenericError styles error tokens.
	GenericError Style = "genericError"
	// GenericErrorInvalid styles invalid tokens.
	GenericErrorInvalid Style = "genericErrorInvalid"
	// GenericErrorUnknown styles unknown tokens.
	GenericErrorUnknown Style = "genericErrorUnknown"
	// GenericInserted styles lines inserted in diff (+).
	GenericInserted Style = "genericInserted"
	// GenericHighlight styles highlights.
	GenericHighlight Style = "genericHighlight"
	// GenericHighlightDim styles dimmed highlights.
	GenericHighlightDim Style = "genericHighlightDim"
	// GenericHeading styles titles.
	GenericHeading Style = "genericHeading"
	// GenericHeadingAccent styles accented titles.
	GenericHeadingAccent Style = "genericHeadingAccent"
	// GenericHeadingSubtle styles de-emphasized titles.
	GenericHeadingSubtle Style = "genericHeadingSubtle"
	// GenericHeadingOK styles success/OK titles.
	GenericHeadingOK Style = "genericHeadingOK"
	// GenericHeadingWarn styles warning titles.
	GenericHeadingWarn Style = "genericHeadingWarn"
	// GenericHeadingError styles error titles.
	GenericHeadingError Style = "genericHeadingError"
	// Literal is a parent style for literal values.
	Literal Style = "literal"
	// LiteralBoolean styles boolean values (true, false).
	LiteralBoolean Style = "literalBoolean"
	// LiteralNull styles null values (~, null).
	LiteralNull Style = "literalNull"
	// LiteralNullImplicit styles implicit null (empty value).
	LiteralNullImplicit Style = "literalNullImplicit"
	// LiteralNumber is a parent style for number values.
	LiteralNumber Style = "literalNumber"
	// LiteralNumberBin styles binary integers (0b...).
	LiteralNumberBin Style = "literalNumberBin"
	// LiteralNumberFloat styles float values (1.5, 2.0).
	LiteralNumberFloat Style = "literalNumberFloat"
	// LiteralNumberHex styles hex integers (0x...).
	LiteralNumberHex Style = "literalNumberHex"
	// LiteralNumberInfinity styles infinity (.inf).
	LiteralNumberInfinity Style = "literalNumberInfinity"
	// LiteralNumberInteger styles integer values (1, 42).
	LiteralNumberInteger Style = "literalNumberInteger"
	// LiteralNumberNaN styles NaN (.nan).
	LiteralNumberNaN Style = "literalNumberNaN"
	// LiteralNumberOct styles octal integers (0o...).
	LiteralNumberOct Style = "literalNumberOct"
	// LiteralString styles unquoted string values.
	LiteralString Style = "literalString"
	// LiteralStringDouble styles double-quoted strings ("...").
	LiteralStringDouble Style = "literalStringDouble"
	// LiteralStringSingle styles single-quoted strings ('...').
	LiteralStringSingle Style = "literalStringSingle"
	// Name is a parent style for names and references.
	Name Style = "name"
	// NameAlias styles aliases (*).
	NameAlias Style = "nameAlias"
	// NameAliasMerge styles merge key (<<).
	NameAliasMerge Style = "nameAliasMerge"
	// NameAnchor styles anchors (&).
	NameAnchor Style = "nameAnchor"
	// NameDecorator styles tags (!tag).
	NameDecorator Style = "nameDecorator"
	// NameTag styles mapping keys (key:).
	NameTag Style = "nameTag"
	// Punctuation is a parent style for punctuation.
	Punctuation Style = "punctuation"
	// PunctuationBlock is a parent style for block scalar punctuation.
	PunctuationBlock Style = "punctuationBlock"
	// PunctuationBlockFolded styles folded block scalar (>).
	PunctuationBlockFolded Style = "punctuationBlockFolded"
	// PunctuationBlockLiteral styles literal block scalar (|).
	PunctuationBlockLiteral Style = "punctuationBlockLiteral"
	// PunctuationCollectEntry styles comma (,).
	PunctuationCollectEntry Style = "punctuationCollectEntry"
	// PunctuationHeading styles document markers (---, ...).
	PunctuationHeading Style = "punctuationHeading"
	// PunctuationMapping is a parent style for mapping punctuation.
	PunctuationMapping Style = "punctuationMapping"
	// PunctuationMappingEnd styles closing brace (}).
	PunctuationMappingEnd Style = "punctuationMappingEnd"
	// PunctuationMappingStart styles opening brace ({).
	PunctuationMappingStart Style = "punctuationMappingStart"
	// PunctuationMappingValue styles colon (:).
	PunctuationMappingValue Style = "punctuationMappingValue"
	// PunctuationSequence is a parent style for sequence punctuation.
	PunctuationSequence Style = "punctuationSequence"
	// PunctuationSequenceEnd styles closing bracket (]).
	PunctuationSequenceEnd Style = "punctuationSequenceEnd"
	// PunctuationSequenceEntry styles sequence entry (-).
	PunctuationSequenceEntry Style = "punctuationSequenceEntry"
	// PunctuationSequenceStart styles opening bracket ([).
	PunctuationSequenceStart Style = "punctuationSequenceStart"
)

var (
	// The inheritance hierarchy for styles. Each style maps to its parent,
	// and [Text] is the root with no parent.
	styleParent = map[Style]Style{
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

	// A shared empty style, returned for lookups of categories that are
	// neither predefined nor set.
	emptyStyle = lipgloss.NewStyle()
)

// getParent returns the parent [Style] for inheritance lookup.
// Returns [Text] if no explicit parent is defined.
func getParent(s Style) Style {
	if p, ok := styleParent[s]; ok {
		return p
	}

	return Text
}

// Styles resolves [Style] categories to [*lipgloss.Style] formatting.
//
// A Styles value holds a base style plus explicit overrides, and resolves every
// predefined category through the inheritance hierarchy when it is built.
// Custom keys, such as overlay styles, are stored as given.
//
// The pointer returned by [Styles.Style] for a category is stable for the life
// of the value, and [Styles.With] keeps the pointers of every category it
// leaves untouched. Renderers rely on that to cache blended styles by pointer.
//
// The zero value resolves every category to an empty style. Create instances
// with [NewStyles].
type Styles struct {
	overrides map[Style]*lipgloss.Style
	resolved  map[Style]*lipgloss.Style
}

// StylesOption configures a [Styles] value during construction.
//
// Available options:
//   - [Set]
type StylesOption func(*Styles)

// Set returns a [StylesOption] that sets the [lipgloss.Style] for a [Style]
// category. Categories below it in the hierarchy inherit it unless they are
// set themselves.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s Style, ls lipgloss.Style) StylesOption {
	return func(st *Styles) {
		st.overrides[s] = &ls
	}
}

// NewStyles creates a new [Styles] value with inheritance resolved.
//
// The base style is used for [Text] and inherited by every other category.
// Use [Set] options to override specific categories; child categories inherit
// from their closest set ancestor.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func NewStyles(base lipgloss.Style, opts ...StylesOption) Styles {
	st := Styles{overrides: map[Style]*lipgloss.Style{Text: &base}}

	for _, opt := range opts {
		opt(&st)
	}

	st.resolved = resolveStyles(st.overrides)

	return st
}

// resolveStyles walks the hierarchy for every predefined category and returns
// the map of category to its closest set ancestor. Custom keys outside the
// hierarchy resolve to themselves. The overrides must hold [Text].
func resolveStyles(overrides map[Style]*lipgloss.Style) map[Style]*lipgloss.Style {
	lookup := func(st Style) *lipgloss.Style {
		for current := st; ; current = getParent(current) {
			if ls, ok := overrides[current]; ok {
				return ls
			}

			if current == Text {
				return overrides[Text]
			}
		}
	}

	resolved := make(map[Style]*lipgloss.Style, len(styleParent)+1+len(overrides))
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

// Style returns the [*lipgloss.Style] for the given [Style] category.
//
// A category that is neither predefined nor set returns an empty style. The
// result is never nil.
func (s Styles) Style(st Style) *lipgloss.Style {
	if ls, ok := s.resolved[st]; ok && ls != nil {
		return ls
	}

	return &emptyStyle
}

// With returns a copy of the [Styles] with the given options applied and
// inheritance resolved again, so overriding a parent category also changes
// the children that inherit from it. The receiver is unchanged.
func (s Styles) With(opts ...StylesOption) Styles {
	c := Styles{overrides: make(map[Style]*lipgloss.Style, len(s.overrides)+len(opts))}
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
