package style

import (
	"maps"

	"charm.land/lipgloss/v2"
)

// Mode represents the color scheme mode of a theme.
//
// Used by theme functions to indicate whether they target light or dark
// backgrounds.
type Mode int

// Color scheme modes.
const (
	Light Mode = iota
	Dark
)

// Style identifies a style category for YAML highlighting.
//
// Style constants are used as keys in [Styles] maps to associate token
// categories with [lipgloss.Style] formatting.
type Style = int

// Style constants for YAML highlighting.
// Names follow Pygments token naming conventions where applicable.
const (
	// Text is a default/fallback style.
	Text Style = iota + 1_000_000
	// Comment styles comments (#).
	Comment
	// CommentPreproc styles preprocessor comment, e.g.: %YAML, %TAG.
	CommentPreproc
	// Generic is a parent style for generic tokens.
	Generic
	// GenericDeleted styles lines deleted in diff (-).
	GenericDeleted
	// GenericError styles error tokens.
	GenericError
	// GenericErrorInvalid styles invalid tokens.
	GenericErrorInvalid
	// GenericErrorUnknown styles unknown tokens.
	GenericErrorUnknown
	// GenericInserted styles lines inserted in diff (+).
	GenericInserted
	// Literal is a parent style for literal values.
	Literal
	// LiteralBoolean styles boolean values (true, false).
	LiteralBoolean
	// LiteralNull styles null values (~, null).
	LiteralNull
	// LiteralNullImplicit styles implicit null (empty value).
	LiteralNullImplicit
	// LiteralNumber is a parent style for number values.
	LiteralNumber
	// LiteralNumberBin styles binary integers (0b...).
	LiteralNumberBin
	// LiteralNumberFloat styles float values (1.5, 2.0).
	LiteralNumberFloat
	// LiteralNumberHex styles hex integers (0x...).
	LiteralNumberHex
	// LiteralNumberInfinity styles infinity (.inf).
	LiteralNumberInfinity
	// LiteralNumberInteger styles integer values (1, 42).
	LiteralNumberInteger
	// LiteralNumberNaN styles NaN (.nan).
	LiteralNumberNaN
	// LiteralNumberOct styles octal integers (0o...).
	LiteralNumberOct
	// LiteralString styles unquoted string values.
	LiteralString
	// LiteralStringDouble styles double-quoted strings ("...").
	LiteralStringDouble
	// LiteralStringSingle styles single-quoted strings ('...').
	LiteralStringSingle
	// Name is a parent style for names and references.
	Name
	// NameAlias styles aliases (*).
	NameAlias
	// NameAliasMerge styles merge key (<<).
	NameAliasMerge
	// NameAnchor styles anchors (&).
	NameAnchor
	// NameDecorator styles tags (!tag).
	NameDecorator
	// NameTag styles mapping keys (key:).
	NameTag
	// Punctuation is a parent style for punctuation.
	Punctuation
	// PunctuationBlock is a parent style for block scalar punctuation.
	PunctuationBlock
	// PunctuationBlockFolded styles folded block scalar (>).
	PunctuationBlockFolded
	// PunctuationBlockLiteral styles literal block scalar (|).
	PunctuationBlockLiteral
	// PunctuationCollectEntry styles comma (,).
	PunctuationCollectEntry
	// PunctuationHeading styles document markers (---, ...).
	PunctuationHeading
	// PunctuationMapping is a parent style for mapping punctuation.
	PunctuationMapping
	// PunctuationMappingEnd styles closing brace (}).
	PunctuationMappingEnd
	// PunctuationMappingStart styles opening brace ({).
	PunctuationMappingStart
	// PunctuationMappingValue styles colon (:).
	PunctuationMappingValue
	// PunctuationSequence is a parent style for sequence punctuation.
	PunctuationSequence
	// PunctuationSequenceEnd styles closing bracket (]).
	PunctuationSequenceEnd
	// PunctuationSequenceEntry styles sequence entry (-).
	PunctuationSequenceEntry
	// PunctuationSequenceStart styles opening bracket ([).
	PunctuationSequenceStart
)

var (
	// StyleParent defines the inheritance hierarchy for styles.
	// Each style maps to its parent style.
	// [Text] is the root and has no parent.
	styleParent = map[Style]Style{
		Comment:                  Text,
		CommentPreproc:           Comment,
		Generic:                  Text,
		GenericDeleted:           Generic,
		GenericError:             Generic,
		GenericErrorInvalid:      GenericError,
		GenericErrorUnknown:      GenericError,
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
	}

	// EmptyStyle is a singleton for missing style lookups.
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

// Styles maps [Style] categories to [*lipgloss.Style] formatting.
// Pointers are stored for stable identity in comparisons.
// Create instances with [NewStyles].
type Styles map[Style]*lipgloss.Style

// StylesOption configures a [Styles] map during construction.
//
// Available options:
//   - [Set]
type StylesOption func(Styles)

// Set returns a [StylesOption] that sets the [lipgloss.Style] for a [Style]
// category.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s Style, ls lipgloss.Style) StylesOption {
	return func(m Styles) {
		m[s] = &ls
	}
}

// NewStyles creates a new [Styles] map with inheritance pre-computed.
//
// The base style is used for [Text] and inherited by all other categories.
// Use [Set] options to override specific categories; child categories inherit
// from their closest defined parent.
//
// Custom style keys (such as overlay kinds) are stored directly without
// inheritance resolution.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func NewStyles(base lipgloss.Style, opts ...StylesOption) Styles {
	overrides := make(Styles)
	overrides[Text] = &base

	for _, opt := range opts {
		opt(overrides)
	}

	// Resolve walks up the inheritance chain to find a defined style.
	resolve := func(s Style) *lipgloss.Style {
		current := s
		for {
			if ls, ok := overrides[current]; ok {
				return ls
			}

			if current == Text {
				break
			}

			current = getParent(current)
		}

		return &base
	}

	// Resolve all predefined styles.
	resolved := make(Styles, len(styleParent)+1+len(overrides))

	resolved[Text] = resolve(Text)
	for st := range styleParent {
		resolved[st] = resolve(st)
	}

	// Include custom keys (not in styleParent) directly.
	// This allows NewStyles to be used for overlay styles with arbitrary keys.
	for st := range overrides {
		if _, isPredefined := styleParent[st]; !isPredefined && st != Text {
			resolved[st] = overrides[st]
		}
	}

	return resolved
}

// Style returns the [*lipgloss.Style] for the given [Style] category.
// Returns an empty [*lipgloss.Style] if the style is not defined.
func (s Styles) Style(st Style) *lipgloss.Style {
	if ls, ok := s[st]; ok {
		return ls
	}

	return &emptyStyle
}

// With returns a copy of the [Styles] with the given options applied.
// The original is not modified.
func (s Styles) With(opts ...StylesOption) Styles {
	result := make(Styles, len(s)+len(opts))
	maps.Copy(result, s)

	for _, opt := range opts {
		opt(result)
	}

	return result
}
