package yamltest

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// XMLStyles implements [niceyaml.StyleGetter] using XML tags instead of ANSI
// escape codes.
//
// Each [style.Style] category wraps content in descriptive tags, making styled
// output easy to compare in tests.
//
// For example, a comment renders as `<comment># text</comment>`.
//
// Create instances with [NewXMLStyles].
type XMLStyles struct {
	only    map[style.Style]bool // If non-nil, only these styles get XML tags.
	exclude map[style.Style]bool // Styles to exclude from XML tagging.
	empty   lipgloss.Style       // Returned for excluded/non-matching styles.
}

// XMLStylesOption configures [XMLStyles].
//
// Available options:
//   - [XMLStyleInclude]
//   - [XMLStyleExclude]
type XMLStylesOption func(*XMLStyles)

// XMLStyleInclude limits XML tags to the given styles.
// All other styles return an empty (no-op) style.
func XMLStyleInclude(styles ...style.Style) XMLStylesOption {
	return func(x *XMLStyles) {
		if x.only == nil {
			x.only = make(map[style.Style]bool)
		}

		for _, s := range styles {
			x.only[s] = true
		}
	}
}

// XMLStyleExclude excludes the given styles from XML tagging.
// Excluded styles return an empty (no-op) style.
func XMLStyleExclude(styles ...style.Style) XMLStylesOption {
	return func(x *XMLStyles) {
		if x.exclude == nil {
			x.exclude = make(map[style.Style]bool)
		}

		for _, s := range styles {
			x.exclude[s] = true
		}
	}
}

// NewXMLStyles creates a new [*XMLStyles] with the given options.
func NewXMLStyles(opts ...XMLStylesOption) *XMLStyles {
	x := &XMLStyles{}

	for _, opt := range opts {
		opt(x)
	}

	return x
}

// Style returns a [*lipgloss.Style] that wraps content in XML tags based on
// the [style.Style] category.
//
// If the style is excluded or not in the "only" list (when configured),
// returns an empty style.
func (x *XMLStyles) Style(s style.Style) *lipgloss.Style {
	// Check if style should be excluded.
	if x.exclude != nil && x.exclude[s] {
		return &x.empty
	}

	// Check if only specific styles are allowed.
	if x.only != nil && !x.only[s] {
		return &x.empty
	}

	tag := styleToTag(s)
	st := lipgloss.NewStyle().Transform(func(content string) string {
		return "<" + tag + ">" + content + "</" + tag + ">"
	})

	return &st
}

// styleToTag maps a [style.Style] to an XML tag name.
func styleToTag(s style.Style) string {
	switch s {
	case style.Text:
		return "text"
	case style.Comment:
		return "comment"
	case style.CommentPreproc:
		return "comment-preproc"
	case style.NameTag:
		return "name-tag"
	case style.NameDecorator:
		return "name-decorator"
	case style.NameAnchor:
		return "name-anchor"
	case style.NameAlias:
		return "name-alias"
	case style.NameAliasMerge:
		return "name-alias-merge"
	case style.GenericInserted:
		return "generic-inserted"
	case style.GenericDeleted:
		return "generic-deleted"
	case style.PunctuationHeading:
		return "punctuation-heading"
	case style.LiteralString:
		return "literal-string"
	case style.LiteralStringSingle:
		return "literal-string-single"
	case style.LiteralStringDouble:
		return "literal-string-double"
	case style.PunctuationBlockLiteral:
		return "punctuation-block-literal"
	case style.PunctuationBlockFolded:
		return "punctuation-block-folded"
	case style.LiteralNumberInteger:
		return "literal-number-integer"
	case style.LiteralNumberFloat:
		return "literal-number-float"
	case style.LiteralNumberBin:
		return "literal-number-bin"
	case style.LiteralNumberOct:
		return "literal-number-oct"
	case style.LiteralNumberHex:
		return "literal-number-hex"
	case style.LiteralNumberInfinity:
		return "literal-number-infinity"
	case style.LiteralNumberNaN:
		return "literal-number-nan"
	case style.LiteralBoolean:
		return "literal-boolean"
	case style.LiteralNull:
		return "literal-null"
	case style.LiteralNullImplicit:
		return "literal-null-implicit"
	case style.GenericError:
		return "generic-error"
	case style.GenericErrorUnknown:
		return "generic-error-unknown"
	case style.GenericErrorInvalid:
		return "generic-error-invalid"
	case style.PunctuationMappingValue:
		return "punctuation-mapping-value"
	case style.PunctuationCollectEntry:
		return "punctuation-collect-entry"
	case style.PunctuationSequenceEntry:
		return "punctuation-sequence-entry"
	case style.PunctuationSequenceStart:
		return "punctuation-sequence-start"
	case style.PunctuationSequenceEnd:
		return "punctuation-sequence-end"
	case style.PunctuationMappingStart:
		return "punctuation-mapping-start"
	case style.PunctuationMappingEnd:
		return "punctuation-mapping-end"
	case style.Generic:
		return "generic"
	case style.Literal:
		return "literal"
	case style.LiteralNumber:
		return "literal-number"
	case style.Name:
		return "name"
	case style.Punctuation:
		return "punctuation"
	case style.PunctuationBlock:
		return "punctuation-block"
	case style.PunctuationMapping:
		return "punctuation-mapping"
	case style.PunctuationSequence:
		return "punctuation-sequence"
	case style.Search:
		return "search"
	case style.SearchSelected:
		return "search-selected"
	default:
		return fmt.Sprintf("style-%d", s)
	}
}
