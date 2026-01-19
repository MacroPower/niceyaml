// Package style provides types and constants for YAML syntax highlighting.
package style

import (
	"maps"

	"charm.land/lipgloss/v2"
)

// Mode represents the color scheme mode of a theme.
type Mode int

// Color scheme modes.
const (
	Light Mode = iota
	Dark
)

// Style identifies a style category for YAML highlighting.
// Used as keys in [Styles] maps.
type Style = int

// Style constants for YAML highlighting.
// Names follow Pygments token naming conventions where applicable.
const (
	Text                     Style = iota // Default/fallback style.
	Comment                               // Comments.
	CommentPreproc                        // Preprocessor comments, e.g.: %YAML, %TAG.
	Generic                               // Generic tokens (parent only).
	GenericDeleted                        // Lines deleted in diff (-).
	GenericError                          // Error tokens.
	GenericErrorInvalid                   // Invalid tokens.
	GenericErrorUnknown                   // Unknown tokens.
	GenericInserted                       // Lines inserted in diff (+).
	Literal                               // Literal values (parent only).
	LiteralBoolean                        // Boolean values.
	LiteralNull                           // Null values, e.g.: ~, null.
	LiteralNullImplicit                   // Implicit null (empty value).
	LiteralNumber                         // Number values (parent only).
	LiteralNumberBin                      // Binary integers (0b...).
	LiteralNumberFloat                    // Float values.
	LiteralNumberHex                      // Hex integers (0x...).
	LiteralNumberInfinity                 // Infinity (.inf).
	LiteralNumberInteger                  // Integer values.
	LiteralNumberNaN                      // NaN (.nan).
	LiteralNumberOct                      // Octal integers (0o...).
	LiteralString                         // Unquoted string values.
	LiteralStringDouble                   // Double-quoted strings.
	LiteralStringSingle                   // Single-quoted strings.
	Name                                  // Names and references (parent only).
	NameAlias                             // Aliases, e.g.: *.
	NameAliasMerge                        // Merge key (<<).
	NameAnchor                            // Anchors, e.g.: &.
	NameDecorator                         // Tags, e.g.: !tag.
	NameTag                               // Mapping keys.
	Punctuation                           // Punctuation (parent only).
	PunctuationBlock                      // Block scalar punctuation (parent only).
	PunctuationBlockFolded                // Folded block scalar (>).
	PunctuationBlockLiteral               // Literal block scalar (|).
	PunctuationCollectEntry               // Comma (,).
	PunctuationHeading                    // Document markers (---, ...).
	PunctuationMapping                    // Mapping punctuation (parent only).
	PunctuationMappingEnd                 // Closing brace (}).
	PunctuationMappingStart               // Opening brace ({).
	PunctuationMappingValue               // Colon (:).
	PunctuationSequence                   // Sequence punctuation (parent only).
	PunctuationSequenceEnd                // Closing bracket (]).
	PunctuationSequenceEntry              // Sequence entry (-).
	PunctuationSequenceStart              // Opening bracket ([).
)

var (
	// StyleParent defines the inheritance hierarchy for styles.
	// Each style maps to its parent style. [Text] is the root and has no parent.
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

// Styles defines styles for YAML highlighting.
type Styles map[Style]lipgloss.Style

// StylesOption configures a [Styles] map during construction.
// See [Set] for the primary option.
type StylesOption func(map[Style]lipgloss.Style)

// Set returns a [StylesOption] that overrides the style for the given [Style].
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s Style, ls lipgloss.Style) StylesOption {
	return func(m map[Style]lipgloss.Style) {
		m[s] = ls
	}
}

// NewStyles creates a [Styles] map with pre-computed entries.
// The base style is used for [Text] and inherited by all other styles.
// Use [Set] options to override specific styles.
//
// For predefined styles in the hierarchy (e.g., [Comment], [LiteralString]),
// styles are resolved using inheritance. Custom style keys (like overlay kinds)
// are stored directly without inheritance resolution.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func NewStyles(base lipgloss.Style, opts ...StylesOption) Styles {
	overrides := make(map[Style]lipgloss.Style)
	overrides[Text] = base

	for _, opt := range opts {
		opt(overrides)
	}

	// Resolve walks up the inheritance chain to find a defined style.
	resolve := func(s Style) lipgloss.Style {
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

		return base
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

// Style returns the [lipgloss.Style] for the given [Style] category.
// Returns an empty [lipgloss.Style] if the style is not defined.
func (s Styles) Style(st Style) *lipgloss.Style {
	if ls, ok := s[st]; ok {
		return &ls
	}

	return &emptyStyle
}

// With returns a new [Styles] with the given options applied.
// This creates a copy; the original [Styles] is not modified.
// Use [Set] to create options that add or override specific styles.
func (s Styles) With(opts ...StylesOption) Styles {
	result := make(Styles, len(s)+len(opts))
	maps.Copy(result, s)

	for _, opt := range opts {
		opt(result)
	}

	return result
}
