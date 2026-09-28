package yamltest

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// XMLStyles implements [go.jacobcolvin.com/niceyaml/style.Styler] using XML
// tags instead of ANSI escape codes.
//
// XMLStyles wraps the content of each [kind.Kind] in a tag named after the
// kind.
//
// For example, a comment renders as `<comment># text</comment>`.
//
// The tags are ordinary text, so lipgloss counts them toward display width.
// A printer that combines XMLStyles with
// [go.jacobcolvin.com/niceyaml/printer.WithWrap] or a padded container style
// wraps and pads by tag length rather than by the visible text, so assert
// width and alignment through a color theme instead.
//
// Create instances with [NewXMLStyles].
type XMLStyles struct {
	only map[kind.Kind]bool // If non-nil, only these styles get XML tags.
}

// XMLStylesOption configures [XMLStyles].
//
// Available options:
//   - [XMLStyleInclude]
type XMLStylesOption func(*XMLStyles)

// XMLStyleInclude is an [XMLStylesOption] that limits XML tags to the given
// styles. For every other style, [XMLStyles.Style] returns an empty style.
func XMLStyleInclude(styles ...kind.Kind) XMLStylesOption {
	return func(x *XMLStyles) {
		if x.only == nil {
			x.only = make(map[kind.Kind]bool)
		}

		for _, s := range styles {
			x.only[s] = true
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

// Style returns a [lipgloss.Style] that wraps content in an XML tag named
// after s.
//
// It returns an empty style when [XMLStyleInclude] limits tags to other
// styles.
func (x *XMLStyles) Style(s kind.Kind) lipgloss.Style {
	// Leave a style outside the include list untagged.
	if x.only != nil && !x.only[s] {
		return lipgloss.NewStyle()
	}

	tag := string(s)

	return lipgloss.NewStyle().Transform(func(content string) string {
		return "<" + tag + ">" + content + "</" + tag + ">"
	})
}
