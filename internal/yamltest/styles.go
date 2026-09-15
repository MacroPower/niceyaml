package yamltest

import (
	"sync"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
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
	// Built styles by category, so Style returns a stable pointer per
	// category as [niceyaml.StyleGetter] requires.
	built map[style.Style]*lipgloss.Style
	empty lipgloss.Style // Returned for excluded/non-matching styles.
	mu    sync.Mutex     // Guards built.
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

	x.mu.Lock()
	defer x.mu.Unlock()

	if st, ok := x.built[s]; ok {
		return st
	}

	if x.built == nil {
		x.built = make(map[style.Style]*lipgloss.Style)
	}

	tag := string(s)

	st := lipgloss.NewStyle().Transform(func(content string) string {
		return "<" + tag + ">" + content + "</" + tag + ">"
	})
	x.built[s] = &st

	return &st
}
