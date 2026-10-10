package yamltest

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"

	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
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
// [go.jacobcolvin.com/niceyaml/printer.WithWrapWidth] or a padded container
// style
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

// NewXMLPrinter creates a new [*printer.Printer] that renders through
// [XMLStyles], with no gutter and an empty container style, so its
// output holds only the tagged text. Options in opts apply after these
// defaults.
func NewXMLPrinter(opts ...printer.Option) *printer.Printer {
	defaults := []printer.Option{
		printer.WithStyles(NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	}

	return printer.New(append(defaults, opts...)...)
}

// NewPlainPrinter creates a new [*printer.Printer] with empty styles, no
// gutter, and an empty container style, so its output holds only the text
// of the lines. Options in opts apply after these defaults.
func NewPlainPrinter(opts ...printer.Option) *printer.Printer {
	defaults := []printer.Option{
		printer.WithStyles(style.Styles{}),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	}

	return printer.New(append(defaults, opts...)...)
}

// ColorHex returns c as a hex string, so colors of different types
// compare by value.
func ColorHex(c color.Color) string {
	cf, _ := colorful.MakeColor(c)

	return cf.Hex()
}

// Contrast returns the WCAG contrast ratio between a and b, from 1 for
// equal colors to 21 for black on white.
func Contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

// luminance returns the WCAG relative luminance of c.
func luminance(c color.Color) float64 {
	cf, _ := colorful.MakeColor(c)
	r, g, b := cf.LinearRgb()

	return 0.2126*r + 0.7152*g + 0.0722*b
}
