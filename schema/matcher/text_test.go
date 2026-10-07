package matcher_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
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
			input:   stringtest.Input(`apiVersion: apps/v1`),
			want:    true,
		},
		"prefix a plain string lacks": {
			matcher: matcher.Text(apiVersionPath, isApps),
			input:   stringtest.Input(`apiVersion: batch/v1`),
			want:    false,
		},
		"prefix of a plain float": {
			// A decode into a string reads 2.0 as "2".
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   stringtest.Input(`swagger: 2.0`),
			want:    true,
		},
		"prefix of a quoted float": {
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   stringtest.Input(`swagger: "2.0"`),
			want:    true,
		},
		"prefix a plain float lacks": {
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   stringtest.Input(`swagger: 3.0`),
			want:    false,
		},
		"prefix a plain integer lacks": {
			matcher: matcher.Text(swaggerPath, isTwo),
			input:   stringtest.Input(`swagger: 2`),
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

	t.Run("context that ended is an error", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		ok, err := matcher.Text(kindPath, acceptAll).Match(ctx, doc)
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, ok)
	})

	t.Run("alias that does not resolve is an error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, `kind: *missing`)

		ok, err := matcher.Text(kindPath, acceptAll).Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrAlias)
		assert.False(t, ok)
	})

	t.Run("wildcard path is an error", func(t *testing.T) {
		t.Parallel()

		m := matcher.Text(paths.Current().Child("items").IndexAll(), acceptAll)
		doc := yamltest.FirstDocument(t, stringtest.Input(`items: [x]`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrWildcard)
	})

	t.Run("alias bomb is an error", func(t *testing.T) {
		t.Parallel()

		// Each level lists the level below ten times, so a decode of the
		// alias key reads 10^7 scalars. Match refuses the document before
		// it decodes anything, as Content does.
		input := yamltest.AliasLevels(7) + "kind:\n  ? *l7\n  : v\n"
		doc := yamltest.FirstDocument(t, input)

		ok, err := matcher.Text(kindPath, acceptAll).Match(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
		assert.False(t, ok)

		// The aliases of the document are the cause, so the document is
		// at fault for the refusal.
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("alias bomb in a source with the limit off is read", func(t *testing.T) {
		t.Parallel()

		// The tag keeps the alias in the node at kind, so Match counts the
		// document before it reads the node, and the levels put it past
		// the limit.
		input := "name: &name Deployment\n" + yamltest.AliasLevels(4) + "kind: !!str *name\n"
		m := matcher.Text(kindPath, func(text string) bool { return text == "Deployment" })

		ok, err := m.Match(t.Context(), yamltest.FirstDocument(t, input))
		require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
		assert.False(t, ok)

		ok, err = m.Match(t.Context(), yamltest.FirstDocument(t, input, niceyaml.WithAliasLimit(false)))
		require.NoError(t, err)
		assert.True(t, ok)
	})
}

func TestText_Reads(t *testing.T) {
	t.Parallel()

	// The text the function receives. A string want of Content matches
	// the same text.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"plain string": {
			input: stringtest.Input(`version: apps/v1`),
			want:  "apps/v1",
		},
		"plain float with a trailing zero": {
			input: stringtest.Input(`version: 2.0`),
			want:  "2.0",
		},
		"plain float with a zero after its fraction": {
			input: stringtest.Input(`version: 3.10`),
			want:  "3.10",
		},
		"plain hex integer": {
			input: stringtest.Input(`version: 0x10`),
			want:  "0x10",
		},
		"plain exponent": {
			input: stringtest.Input(`version: 1e3`),
			want:  "1e3",
		},
		"plain infinity": {
			input: stringtest.Input(`version: .inf`),
			want:  ".inf",
		},
		"plain capitalized bool": {
			input: stringtest.Input(`version: True`),
			want:  "True",
		},
		"plain date": {
			input: stringtest.Input(`version: 2001-12-14`),
			want:  "2001-12-14",
		},
		"plain scalar over two lines": {
			input: "version: a\n  b\n",
			want:  "a b",
		},
		"double-quoted number": {
			input: stringtest.Input(`version: "2.0"`),
			want:  "2.0",
		},
		"double-quoted escape": {
			input: stringtest.Input(`version: "a\tb"`),
			want:  "a\tb",
		},
		"single-quoted quote": {
			input: stringtest.Input(`version: 'it''s'`),
			want:  "it's",
		},
		"empty string": {
			input: stringtest.Input(`version: ""`),
			want:  "",
		},
		"literal block": {
			input: "version: |\n  1.10\n",
			want:  "1.10\n",
		},
		"literal block that strips its line break": {
			input: "version: |-\n  1.10\n",
			want:  "1.10",
		},
		"folded block": {
			input: "version: >\n  a\n  b\n",
			want:  "a b\n",
		},
		"anchored float": {
			input: stringtest.Input(`version: &v 1.10`),
			want:  "1.10",
		},
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
		"str-tagged float": {
			input: stringtest.Input(`version: !!str 1.10`),
			want:  "1.10",
		},
		"float-tagged float": {
			input: stringtest.Input(`version: !!float 1.10`),
			want:  "1.10",
		},
		"custom-tagged float": {
			input: stringtest.Input(`version: !custom 1.10`),
			want:  "1.10",
		},
		"float-tagged quoted scalar": {
			// The tag makes the quoted text a float, which the decoder
			// respells.
			input: stringtest.Input(`version: !!float "1.10"`),
			want:  "1.1",
		},
		"bool-tagged quoted scalar": {
			input: stringtest.Input(`version: !!bool "True"`),
			want:  "true",
		},
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

// acceptAll is a function for [matcher.Text] that accepts any text.
func acceptAll(string) bool {
	return true
}
