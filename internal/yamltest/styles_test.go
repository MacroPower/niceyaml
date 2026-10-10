package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
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
