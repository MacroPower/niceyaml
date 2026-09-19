package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestNewXMLStyles(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	assert.NotNil(t, getter)
}

func TestXMLStyles_Style(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input kind.Kind
		want  string
	}{
		"Text": {
			input: kind.Text,
			want:  "<text>test</text>",
		},
		"NameTag": {
			input: kind.NameTag,
			want:  "<nameTag>test</nameTag>",
		},
		"LiteralString": {
			input: kind.LiteralString,
			want:  "<literalString>test</literalString>",
		},
		"LiteralNumberInteger": {
			input: kind.LiteralNumberInteger,
			want:  "<literalNumberInteger>test</literalNumberInteger>",
		},
		"LiteralBoolean": {
			input: kind.LiteralBoolean,
			want:  "<literalBoolean>test</literalBoolean>",
		},
		"LiteralNull": {
			input: kind.LiteralNull,
			want:  "<literalNull>test</literalNull>",
		},
		"NameAnchor": {
			input: kind.NameAnchor,
			want:  "<nameAnchor>test</nameAnchor>",
		},
		"NameAlias": {
			input: kind.NameAlias,
			want:  "<nameAlias>test</nameAlias>",
		},
		"Comment": {
			input: kind.Comment,
			want:  "<comment>test</comment>",
		},
		"GenericError": {
			input: kind.GenericError,
			want:  "<genericError>test</genericError>",
		},
		"NameDecorator": {
			input: kind.NameDecorator,
			want:  "<nameDecorator>test</nameDecorator>",
		},
		"PunctuationHeading": {
			input: kind.PunctuationHeading,
			want:  "<punctuationHeading>test</punctuationHeading>",
		},
		"CommentPreproc": {
			input: kind.CommentPreproc,
			want:  "<commentPreproc>test</commentPreproc>",
		},
		"PunctuationSequenceEntry": {
			input: kind.PunctuationSequenceEntry,
			want:  "<punctuationSequenceEntry>test</punctuationSequenceEntry>",
		},
		"PunctuationBlockLiteral": {
			input: kind.PunctuationBlockLiteral,
			want:  "<punctuationBlockLiteral>test</punctuationBlockLiteral>",
		},
		"GenericInserted": {
			input: kind.GenericInserted,
			want:  "<genericInserted>test</genericInserted>",
		},
		"GenericDeleted": {
			input: kind.GenericDeleted,
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
	st := getter.Style(kind.Kind("unknownStyle"))

	require.NotNil(t, st)

	got := st.Render("test")
	assert.Equal(t, "<unknownStyle>test</unknownStyle>", got)
}

func TestXMLStyles_Style_EmptyContent(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	st := getter.Style(kind.NameTag)

	require.NotNil(t, st)

	got := st.Render("")
	assert.Equal(t, "<nameTag></nameTag>", got)
}

func TestXMLStyles_XMLStyleInclude(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles(
		yamltest.XMLStyleInclude(kind.GenericHighlightDim, kind.GenericHighlight),
	)

	// Included styles get XML tags.
	searchStyle := getter.Style(kind.GenericHighlightDim)
	require.NotNil(t, searchStyle)
	assert.Equal(t, "<genericHighlightDim>test</genericHighlightDim>", searchStyle.Render("test"))

	selectedStyle := getter.Style(kind.GenericHighlight)
	require.NotNil(t, selectedStyle)
	assert.Equal(t, "<genericHighlight>test</genericHighlight>", selectedStyle.Render("test"))

	// Non-included styles return empty (no transformation).
	commentStyle := getter.Style(kind.Comment)
	require.NotNil(t, commentStyle)
	assert.Equal(t, "test", commentStyle.Render("test"))
}

func TestXMLStyles_XMLStyleExclude(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles(
		yamltest.XMLStyleExclude(kind.Text, kind.Comment),
	)

	// Excluded styles return empty (no transformation).
	textStyle := getter.Style(kind.Text)
	require.NotNil(t, textStyle)
	assert.Equal(t, "test", textStyle.Render("test"))

	commentStyle := getter.Style(kind.Comment)
	require.NotNil(t, commentStyle)
	assert.Equal(t, "test", commentStyle.Render("test"))

	// Non-excluded styles get XML tags.
	searchStyle := getter.Style(kind.GenericHighlightDim)
	require.NotNil(t, searchStyle)
	assert.Equal(t, "<genericHighlightDim>test</genericHighlightDim>", searchStyle.Render("test"))
}
