package niceyaml

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Style identifies a style category for YAML highlighting.
// Used as keys in [Styles] maps.
type Style int

// Style constants for YAML highlighting.
const (
	StyleDefault      Style = iota // Default/fallback style.
	StyleKey                       // Mapping keys.
	StyleString                    // String values.
	StyleNumber                    // Numeric values.
	StyleBool                      // Boolean values.
	StyleNull                      // E.g.: ~, null.
	StyleAnchor                    // E.g.: &.
	StyleAlias                     // E.g.: *, <<.
	StyleComment                   // Comments.
	StyleError                     // Error highlighting.
	StyleTag                       // E.g.: !tag.
	StyleDocument                  // E.g.: ---.
	StyleDirective                 // E.g.: %YAML, %TAG.
	StylePunctuation               // E.g.: -, :, ?, [, ], {, }.
	StyleBlockScalar               // E.g.: |, >.
	StyleDiffInserted              // Lines inserted in diff (+).
	StyleDiffDeleted               // Lines deleted in diff (-).
)

// Styles defines styles for YAML highlighting.
// See [DefaultStyles] for the default style set.
type Styles map[Style]lipgloss.Style

// Style returns the [lipgloss.Style] for the given [Style] category.
// If the given [Style] is not defined, it returns [StyleDefault].
// If no [StyleDefault] is defined, it returns an empty [lipgloss.Style].
func (s Styles) Style(style Style) *lipgloss.Style {
	matchedStyle, ok := s[style]
	if ok {
		return &matchedStyle
	}

	defaultStyle, ok := s[StyleDefault]
	if ok {
		return &defaultStyle
	}

	return &lipgloss.Style{}
}

// DefaultStyles returns [Styles] using CharmTone colors.
func DefaultStyles() Styles {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return Styles{
		StyleDefault:      base,
		StyleKey:          base.Foreground(charmtone.Mauve),  // NameTag.
		StyleString:       base.Foreground(charmtone.Cumin),  // LiteralString.
		StyleNumber:       base.Foreground(charmtone.Julep),  // LiteralNumber.
		StyleBool:         base.Foreground(charmtone.Malibu), // KeywordConstant.
		StyleNull:         base.Foreground(charmtone.Malibu), // KeywordConstant.
		StyleAnchor:       base.Foreground(charmtone.Bengal), // CommentPreproc.
		StyleAlias:        base.Foreground(charmtone.Bengal), // CommentPreproc.
		StyleComment:      base.Foreground(charmtone.Oyster), // Comment.
		StyleTag:          base.Foreground(charmtone.Bengal), // CommentPreproc.
		StyleDocument:     base.Foreground(charmtone.Smoke),  // NameNamespace.
		StyleDirective:    base.Foreground(charmtone.Smoke),  // NameNamespace.
		StylePunctuation:  base.Foreground(charmtone.Zest),   // Punctuation.
		StyleBlockScalar:  base.Foreground(charmtone.Zest),   // Punctuation.
		StyleError:        base.Foreground(charmtone.Butter).Background(charmtone.Sriracha),
		StyleDiffInserted: base.Foreground(charmtone.Julep).Background(charmtone.Spinach), // GenericInserted.
		StyleDiffDeleted:  base.Foreground(charmtone.Cherry).Background(charmtone.Toast),  // GenericDeleted.
	}
}

// stylesEqual compares two styles for equality (for span grouping purposes).
// Two styles are equal if they produce the same visual output.
func stylesEqual(s1, s2 *lipgloss.Style) bool {
	// Compare rendered output of a test string.
	// This is a pragmatic approach since lipgloss doesn't expose style internals.
	const testStr = "x"
	return s1.Render(testStr) == s2.Render(testStr)
}
