package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml/style"
	"jacobcolvin.com/niceyaml/yamltest"
)

func TestNewXMLStyles(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	assert.NotNil(t, getter)
}

func TestXMLStyles_Style(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input style.Style
		want  string
	}{
		"Text": {
			input: style.Text,
			want:  "<text>test</text>",
		},
		"NameTag": {
			input: style.NameTag,
			want:  "<name-tag>test</name-tag>",
		},
		"LiteralString": {
			input: style.LiteralString,
			want:  "<literal-string>test</literal-string>",
		},
		"LiteralNumberInteger": {
			input: style.LiteralNumberInteger,
			want:  "<literal-number-integer>test</literal-number-integer>",
		},
		"LiteralBoolean": {
			input: style.LiteralBoolean,
			want:  "<literal-boolean>test</literal-boolean>",
		},
		"LiteralNull": {
			input: style.LiteralNull,
			want:  "<literal-null>test</literal-null>",
		},
		"NameAnchor": {
			input: style.NameAnchor,
			want:  "<name-anchor>test</name-anchor>",
		},
		"NameAlias": {
			input: style.NameAlias,
			want:  "<name-alias>test</name-alias>",
		},
		"Comment": {
			input: style.Comment,
			want:  "<comment>test</comment>",
		},
		"GenericError": {
			input: style.GenericError,
			want:  "<generic-error>test</generic-error>",
		},
		"NameDecorator": {
			input: style.NameDecorator,
			want:  "<name-decorator>test</name-decorator>",
		},
		"PunctuationHeading": {
			input: style.PunctuationHeading,
			want:  "<punctuation-heading>test</punctuation-heading>",
		},
		"CommentPreproc": {
			input: style.CommentPreproc,
			want:  "<comment-preproc>test</comment-preproc>",
		},
		"PunctuationSequenceEntry": {
			input: style.PunctuationSequenceEntry,
			want:  "<punctuation-sequence-entry>test</punctuation-sequence-entry>",
		},
		"PunctuationBlockLiteral": {
			input: style.PunctuationBlockLiteral,
			want:  "<punctuation-block-literal>test</punctuation-block-literal>",
		},
		"GenericInserted": {
			input: style.GenericInserted,
			want:  "<generic-inserted>test</generic-inserted>",
		},
		"GenericDeleted": {
			input: style.GenericDeleted,
			want:  "<generic-deleted>test</generic-deleted>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			getter := yamltest.NewXMLStyles()
			st := getter.Style(tc.input)

			require.NotNil(t, st)

			got := st.Render("test")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestXMLStyles_Style_UnknownStyle(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	st := getter.Style(style.Style(999))

	require.NotNil(t, st)

	got := st.Render("test")
	assert.Equal(t, "<style-999>test</style-999>", got)
}

func TestXMLStyles_Style_EmptyContent(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	st := getter.Style(style.NameTag)

	require.NotNil(t, st)

	got := st.Render("")
	assert.Equal(t, "<name-tag></name-tag>", got)
}
