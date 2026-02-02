package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style"
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
			want:  "<nameTag>test</nameTag>",
		},
		"LiteralString": {
			input: style.LiteralString,
			want:  "<literalString>test</literalString>",
		},
		"LiteralNumberInteger": {
			input: style.LiteralNumberInteger,
			want:  "<literalNumberInteger>test</literalNumberInteger>",
		},
		"LiteralBoolean": {
			input: style.LiteralBoolean,
			want:  "<literalBoolean>test</literalBoolean>",
		},
		"LiteralNull": {
			input: style.LiteralNull,
			want:  "<literalNull>test</literalNull>",
		},
		"NameAnchor": {
			input: style.NameAnchor,
			want:  "<nameAnchor>test</nameAnchor>",
		},
		"NameAlias": {
			input: style.NameAlias,
			want:  "<nameAlias>test</nameAlias>",
		},
		"Comment": {
			input: style.Comment,
			want:  "<comment>test</comment>",
		},
		"GenericError": {
			input: style.GenericError,
			want:  "<genericError>test</genericError>",
		},
		"NameDecorator": {
			input: style.NameDecorator,
			want:  "<nameDecorator>test</nameDecorator>",
		},
		"PunctuationHeading": {
			input: style.PunctuationHeading,
			want:  "<punctuationHeading>test</punctuationHeading>",
		},
		"CommentPreproc": {
			input: style.CommentPreproc,
			want:  "<commentPreproc>test</commentPreproc>",
		},
		"PunctuationSequenceEntry": {
			input: style.PunctuationSequenceEntry,
			want:  "<punctuationSequenceEntry>test</punctuationSequenceEntry>",
		},
		"PunctuationBlockLiteral": {
			input: style.PunctuationBlockLiteral,
			want:  "<punctuationBlockLiteral>test</punctuationBlockLiteral>",
		},
		"GenericInserted": {
			input: style.GenericInserted,
			want:  "<genericInserted>test</genericInserted>",
		},
		"GenericDeleted": {
			input: style.GenericDeleted,
			want:  "<genericDeleted>test</genericDeleted>",
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
	st := getter.Style(style.Style("unknownStyle"))

	require.NotNil(t, st)

	got := st.Render("test")
	assert.Equal(t, "<unknownStyle>test</unknownStyle>", got)
}

func TestXMLStyles_Style_EmptyContent(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	st := getter.Style(style.NameTag)

	require.NotNil(t, st)

	got := st.Render("")
	assert.Equal(t, "<nameTag></nameTag>", got)
}

func TestXMLStyles_XMLStyleInclude(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles(
		yamltest.XMLStyleInclude(style.Search, style.SearchSelected),
	)

	// Included styles get XML tags.
	searchStyle := getter.Style(style.Search)
	require.NotNil(t, searchStyle)
	assert.Equal(t, "<search>test</search>", searchStyle.Render("test"))

	selectedStyle := getter.Style(style.SearchSelected)
	require.NotNil(t, selectedStyle)
	assert.Equal(t, "<searchSelected>test</searchSelected>", selectedStyle.Render("test"))

	// Non-included styles return empty (no transformation).
	commentStyle := getter.Style(style.Comment)
	require.NotNil(t, commentStyle)
	assert.Equal(t, "test", commentStyle.Render("test"))
}

func TestXMLStyles_XMLStyleExclude(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles(
		yamltest.XMLStyleExclude(style.Text, style.Comment),
	)

	// Excluded styles return empty (no transformation).
	textStyle := getter.Style(style.Text)
	require.NotNil(t, textStyle)
	assert.Equal(t, "test", textStyle.Render("test"))

	commentStyle := getter.Style(style.Comment)
	require.NotNil(t, commentStyle)
	assert.Equal(t, "test", commentStyle.Render("test"))

	// Non-excluded styles get XML tags.
	searchStyle := getter.Style(style.Search)
	require.NotNil(t, searchStyle)
	assert.Equal(t, "<search>test</search>", searchStyle.Render("test"))
}
