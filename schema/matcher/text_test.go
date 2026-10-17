package matcher_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestText(t *testing.T) {
	t.Parallel()

	swaggerPath := paths.Current().Child("swagger")
	isTwo := func(text string) bool { return strings.HasPrefix(text, "2.") }
	isApps := func(text string) bool { return strings.HasPrefix(text, "apps/") }

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		want    bool
	}{
		"prefix of a plain string": {
			matcher: matcher.Text(apiVersionPath, isApps),
			input:   `apiVersion: apps/v1`,
			want:    true,
		},
		"prefix a plain string lacks": {
			matcher: matcher.Text(apiVersionPath, isApps),
			input:   `apiVersion: batch/v1`,
			want:    false,
		},
		"prefix of a plain float": {
			// A decode into a string reads 2.0 as "2".
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   `swagger: 2.0`,
			want:    true,
		},
		"prefix of a quoted float": {
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   `swagger: "2.0"`,
			want:    true,
		},
		"prefix a plain float lacks": {
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   `swagger: 3.0`,
			want:    false,
		},
		"prefix a plain integer lacks": {
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   `swagger: 2`,
			want:    false,
		},
		"nested path": {
			matcher: matcher.Text(metadataName, func(text string) bool { return strings.HasSuffix(text, "-app") }),
			input: stringtest.Input(`
				metadata:
				  name: my-app
			`),
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			assert.Equal(t, tc.want, match(t, tc.matcher, doc))
		})
	}

	t.Run("nil function panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.Text: match is nil", func() {
			matcher.Text(kindPath, nil)
		})
	})

	testCannotDecide(t, func(path paths.Path) matcher.Matcher {
		return matcher.Text(path, acceptAll)
	})

	testAliasBomb(t, matcher.Text(kindPath, func(text string) bool { return text == "Deployment" }))
}

func TestText_Reads(t *testing.T) {
	t.Parallel()

	// The text the function receives. A string want of Content matches
	// the same text.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"plain string":                               {input: `version: apps/v1`, want: "apps/v1"},
		"plain float with a trailing zero":           {input: `version: 2.0`, want: "2.0"},
		"plain float with a zero after its fraction": {input: `version: 3.10`, want: "3.10"},
		"plain hex integer":                          {input: `version: 0x10`, want: "0x10"},
		"plain exponent":                             {input: `version: 1e3`, want: "1e3"},
		"plain infinity":                             {input: `version: .inf`, want: ".inf"},
		"plain capitalized bool":                     {input: `version: True`, want: "True"},
		"plain date":                                 {input: `version: 2001-12-14`, want: "2001-12-14"},
		"plain scalar over two lines":                {input: stringtest.LinesLF("version: a", "  b"), want: "a b"},
		"double-quoted number":                       {input: `version: "2.0"`, want: "2.0"},
		"double-quoted escape":                       {input: `version: "a\tb"`, want: "a\tb"},
		"single-quoted quote":                        {input: `version: 'it''s'`, want: "it's"},
		"empty string":                               {input: `version: ""`, want: ""},
		"literal block": {
			input: stringtest.LinesLF("version: |", "  1.10"),
			want:  "1.10\n",
		},
		"literal block that strips its line break": {
			input: stringtest.LinesLF("version: |-", "  1.10"),
			want:  "1.10",
		},
		"folded block": {
			input: stringtest.LinesLF("version: >", "  a", "  b"),
			want:  "a b\n",
		},
		"anchored float": {input: `version: &v 1.10`, want: "1.10"},
		"aliased float": {
			input: stringtest.Input(`
				v: &v 1.10
				version: *v
			`),
			want: "1.10",
		},
		"float behind a tagged alias": {
			input: stringtest.Input(`
				v: &v 1.10
				version: !t *v
			`),
			want: "1.10",
		},
		"str-tagged float":    {input: `version: !!str 1.10`, want: "1.10"},
		"float-tagged float":  {input: `version: !!float 1.10`, want: "1.10"},
		"custom-tagged float": {input: `version: !custom 1.10`, want: "1.10"},
		"float-tagged quoted scalar": {
			// The tag makes the quoted text a float, which the decoder
			// respells.
			input: `version: !!float "1.10"`,
			want:  "1.1",
		},
		"bool-tagged quoted scalar": {input: `version: !!bool "True"`, want: "true"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			var got []string

			m := matcher.Text(versionPath, func(text string) bool {
				got = append(got, text)

				return true
			})

			assert.True(t, match(t, m, doc))
			assert.Equal(t, []string{tc.want}, got)

			assert.True(t, match(t, matcher.Content(versionPath, tc.want), doc))
			assert.False(t, match(t, matcher.Content(versionPath, tc.want+"x"), doc))
		})
	}
}

func TestText_NoText(t *testing.T) {
	t.Parallel()

	// A node that holds no text does not match, and the function never
	// runs for it.
	tcs := map[string]struct {
		input string
	}{
		"null":                    {input: `version: null`},
		"tilde":                   {input: `version: ~`},
		"no value":                {input: `version:`},
		"null-tagged scalar":      {input: `version: !!null x`},
		"alias to a null":         {input: "n: &n ~\nversion: *n"},
		"mapping":                 {input: `version: {major: 1}`},
		"empty mapping":           {input: `version: {}`},
		"sequence":                {input: `version: [1.10]`},
		"empty sequence":          {input: `version: []`},
		"alias to a sequence":     {input: "s: &s [1]\nversion: *s"},
		"timestamp-tagged scalar": {input: `version: !!timestamp 2001-12-14`},
		"missing path":            {input: `kind: Deployment`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			called := false
			m := matcher.Text(versionPath, func(string) bool {
				called = true

				return true
			})

			assert.False(t, match(t, m, doc))
			assert.False(t, called)
		})
	}
}

func TestText_Describe(t *testing.T) {
	t.Parallel()

	m := matcher.Text(paths.Doc().Child("apiVersion"), acceptAll)
	assert.Equal(t, "$.apiVersion passes a text test", description(t, m))
}

// acceptAll is a function for [matcher.Text] that accepts any text.
func acceptAll(string) bool {
	return true
}
