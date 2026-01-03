package yamltest

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml"
)

// XMLStyles implements [niceyaml.StyleGetter] using XML-like tags.
// Each style category wraps content in descriptive tags, e.g., `<comment># text</comment>`.
// This is useful for testing styled output without dealing with ANSI escape codes.
// Create instances with [NewXMLStyles].
type XMLStyles struct{}

// NewXMLStyles creates a new [XMLStyles].
func NewXMLStyles() XMLStyles {
	return XMLStyles{}
}

// GetStyle returns a [*lipgloss.Style] that wraps content in XML tags based on the style category.
func (x XMLStyles) GetStyle(s niceyaml.Style) *lipgloss.Style {
	tag := styleToTag(s)
	style := lipgloss.NewStyle().Transform(func(content string) string {
		return "<" + tag + ">" + content + "</" + tag + ">"
	})

	return &style
}

// styleToTag maps a [niceyaml.Style] to an XML tag name.
func styleToTag(s niceyaml.Style) string {
	switch s {
	case niceyaml.StyleDefault:
		return "default"
	case niceyaml.StyleKey:
		return "key"
	case niceyaml.StyleString:
		return "string"
	case niceyaml.StyleNumber:
		return "number"
	case niceyaml.StyleBool:
		return "bool"
	case niceyaml.StyleNull:
		return "null"
	case niceyaml.StyleAnchor:
		return "anchor"
	case niceyaml.StyleAlias:
		return "alias"
	case niceyaml.StyleComment:
		return "comment"
	case niceyaml.StyleError:
		return "error"
	case niceyaml.StyleTag:
		return "tag"
	case niceyaml.StyleDocument:
		return "document"
	case niceyaml.StyleDirective:
		return "directive"
	case niceyaml.StylePunctuation:
		return "punctuation"
	case niceyaml.StyleBlockScalar:
		return "block-scalar"
	case niceyaml.StyleDiffInserted:
		return "diff-inserted"
	case niceyaml.StyleDiffDeleted:
		return "diff-deleted"
	default:
		return "unknown"
	}
}
