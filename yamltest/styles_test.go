package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/yamltest"
)

func TestNewXMLStyles(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	assert.NotNil(t, getter)
}

func TestXMLStyles_Style(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		want  string
		input niceyaml.Style
	}{
		"StyleDefault": {
			input: niceyaml.StyleDefault,
			want:  "<default>test</default>",
		},
		"StyleKey": {
			input: niceyaml.StyleKey,
			want:  "<key>test</key>",
		},
		"StyleString": {
			input: niceyaml.StyleString,
			want:  "<string>test</string>",
		},
		"StyleNumber": {
			input: niceyaml.StyleNumber,
			want:  "<number>test</number>",
		},
		"StyleBool": {
			input: niceyaml.StyleBool,
			want:  "<bool>test</bool>",
		},
		"StyleNull": {
			input: niceyaml.StyleNull,
			want:  "<null>test</null>",
		},
		"StyleAnchor": {
			input: niceyaml.StyleAnchor,
			want:  "<anchor>test</anchor>",
		},
		"StyleAlias": {
			input: niceyaml.StyleAlias,
			want:  "<alias>test</alias>",
		},
		"StyleComment": {
			input: niceyaml.StyleComment,
			want:  "<comment>test</comment>",
		},
		"StyleError": {
			input: niceyaml.StyleError,
			want:  "<error>test</error>",
		},
		"StyleTag": {
			input: niceyaml.StyleTag,
			want:  "<tag>test</tag>",
		},
		"StyleDocument": {
			input: niceyaml.StyleDocument,
			want:  "<document>test</document>",
		},
		"StyleDirective": {
			input: niceyaml.StyleDirective,
			want:  "<directive>test</directive>",
		},
		"StylePunctuation": {
			input: niceyaml.StylePunctuation,
			want:  "<punctuation>test</punctuation>",
		},
		"StyleBlockScalar": {
			input: niceyaml.StyleBlockScalar,
			want:  "<block-scalar>test</block-scalar>",
		},
		"StyleDiffInserted": {
			input: niceyaml.StyleDiffInserted,
			want:  "<diff-inserted>test</diff-inserted>",
		},
		"StyleDiffDeleted": {
			input: niceyaml.StyleDiffDeleted,
			want:  "<diff-deleted>test</diff-deleted>",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			getter := yamltest.NewXMLStyles()
			style := getter.Style(tc.input)

			require.NotNil(t, style)

			got := style.Render("test")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestXMLStyles_Style_UnknownStyle(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	style := getter.Style(niceyaml.Style(999))

	require.NotNil(t, style)

	got := style.Render("test")
	assert.Equal(t, "<unknown>test</unknown>", got)
}

func TestXMLStyles_Style_EmptyContent(t *testing.T) {
	t.Parallel()

	getter := yamltest.NewXMLStyles()
	style := getter.Style(niceyaml.StyleKey)

	require.NotNil(t, style)

	got := style.Render("")
	assert.Equal(t, "<key></key>", got)
}
