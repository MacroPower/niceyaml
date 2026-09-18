package yamltest

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// XMLStyles implements [printer.StyleGetter] using XML tags instead of ANSI
// escape codes.
//
// Each [style.Kind] category wraps content in descriptive tags, making styled
// output easy to compare in tests.
//
// For example, a comment renders as `<comment># text</comment>`.
//
// The tags are ordinary text, so lipgloss counts them toward display width.
// A printer that combines XMLStyles with [printer.WithWidth] or a padded
// container style wraps and pads by tag length rather than by the visible
// text, so assert width and alignment through a color theme instead.
//
// Create instances with [NewXMLStyles].
type XMLStyles struct {
	only    map[style.Kind]bool // If non-nil, only these styles get XML tags.
	exclude map[style.Kind]bool // Styles to exclude from XML tagging.
}

// XMLStylesOption configures [XMLStyles].
//
// Available options:
//   - [XMLStyleInclude]
//   - [XMLStyleExclude]
type XMLStylesOption func(*XMLStyles)

// XMLStyleInclude is an [XMLStylesOption] that limits XML tags to the given
// styles. All other styles return an empty (no-op) style.
func XMLStyleInclude(styles ...style.Kind) XMLStylesOption {
	return func(x *XMLStyles) {
		if x.only == nil {
			x.only = make(map[style.Kind]bool)
		}

		for _, s := range styles {
			x.only[s] = true
		}
	}
}

// XMLStyleExclude is an [XMLStylesOption] that excludes the given styles
// from XML tagging. Excluded styles return an empty (no-op) style.
func XMLStyleExclude(styles ...style.Kind) XMLStylesOption {
	return func(x *XMLStyles) {
		if x.exclude == nil {
			x.exclude = make(map[style.Kind]bool)
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

// Style returns a [lipgloss.Style] that wraps content in XML tags based on
// the [style.Kind] category.
//
// If the style is excluded or not in the "only" list (when configured),
// returns an empty style.
func (x *XMLStyles) Style(s style.Kind) lipgloss.Style {
	// Check if style should be excluded.
	if x.exclude != nil && x.exclude[s] {
		return lipgloss.NewStyle()
	}

	// Check if only specific styles are allowed.
	if x.only != nil && !x.only[s] {
		return lipgloss.NewStyle()
	}

	tag := string(s)

	return lipgloss.NewStyle().Transform(func(content string) string {
		return "<" + tag + ">" + content + "</" + tag + ">"
	})
}
