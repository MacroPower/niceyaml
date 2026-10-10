package yamltest_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestXMLStyles_Style(t *testing.T) {
	t.Parallel()

	var _ style.Styler = (*yamltest.XMLStyles)(nil)

	include := []yamltest.XMLStylesOption{
		yamltest.XMLStyleInclude(kind.GenericHighlightDim, kind.GenericHighlight),
	}

	tcs := map[string]struct {
		input   kind.Kind
		content string
		want    string
		opts    []yamltest.XMLStylesOption
	}{
		"text": {
			input:   kind.Text,
			content: "test",
			want:    "<text>test</text>",
		},
		"kind of several words": {
			input:   kind.PunctuationSequenceEntry,
			content: "test",
			want:    "<punctuationSequenceEntry>test</punctuationSequenceEntry>",
		},
		"unknown kind": {
			input:   kind.Kind("unknownStyle"),
			content: "test",
			want:    "<unknownStyle>test</unknownStyle>",
		},
		"empty content": {
			input: kind.NameTag,
			want:  "<nameTag></nameTag>",
		},
		"first included kind": {
			opts:    include,
			input:   kind.GenericHighlightDim,
			content: "test",
			want:    "<genericHighlightDim>test</genericHighlightDim>",
		},
		"second included kind": {
			opts:    include,
			input:   kind.GenericHighlight,
			content: "test",
			want:    "<genericHighlight>test</genericHighlight>",
		},
		"kind outside the include list": {
			opts:    include,
			input:   kind.Comment,
			content: "test",
			want:    "test",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := yamltest.NewXMLStyles(tc.opts...).Style(tc.input).Render(tc.content)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewXMLPrinter(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("a: 1\n").View()

	tcs := map[string]struct {
		want string
		opts []printer.Option
	}{
		"defaults": {
			want: "<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue>" +
				"<text> </text><literalNumberInteger>1</literalNumberInteger>",
		},
		"option after the defaults": {
			opts: []printer.Option{printer.WithStyles(style.Styles{})},
			want: "a: 1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.NewXMLPrinter(tc.opts...).Print(view))
		})
	}
}

func TestColorHex(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "#ff0000", yamltest.ColorHex(lipgloss.Color("#f00")))
	assert.Equal(t, "#ff0000", yamltest.ColorHex(color.RGBA{R: 0xff, A: 0xff}))
}

func TestContrast(t *testing.T) {
	t.Parallel()

	black, white := lipgloss.Color("#000000"), lipgloss.Color("#ffffff")

	tcs := map[string]struct {
		a, b color.Color
		want float64
	}{
		"equal colors":   {a: white, b: white, want: 1},
		"black on white": {a: black, b: white, want: 21},
		"white on black": {a: white, b: black, want: 21},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.InDelta(t, tc.want, yamltest.Contrast(tc.a, tc.b), 1e-9)
		})
	}
}
