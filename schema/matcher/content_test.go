package matcher_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		want    bool
	}{
		"string match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: Deployment`),
			want:    true,
		},
		"string no match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: Service`),
			want:    false,
		},
		"missing field": {
			matcher: matcher.Content(missingPath, "value"),
			input:   stringtest.Input(`kind: Deployment`),
			want:    false,
		},
		"string matches number text": {
			matcher: matcher.Content(versionPath, "2"),
			input:   stringtest.Input(`version: 2`),
			want:    true,
		},
		"uncomparable dynamic type does not match": {
			// T is any, so the compared values may hold a map, which ==
			// cannot compare; the matcher declines rather than panics.
			matcher: matcher.Content[any](kindPath, map[string]any{"a": uint64(1)}),
			input:   stringtest.Input("kind:\n  a: 1"),
			want:    false,
		},
		"float matches unquoted float": {
			matcher: matcher.Content(versionPath, 1.0),
			input:   stringtest.Input(`version: 1.0`),
			want:    true,
		},
		"float matches integer spelling": {
			matcher: matcher.Content(versionPath, 1.0),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"int no match": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: 3`),
			want:    false,
		},
		"bool match": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: true`),
			want:    true,
		},
		"bool does not match string": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: "true"`),
			want:    false,
		},
		"value that does not decode": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: abc`),
			want:    false,
		},
		"mapping does not decode into scalar": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input: stringtest.Input(`
				kind:
				  name: Deployment
			`),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			got := match(t, tc.matcher, doc)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nested path match", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(metadataName, "my-app")
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			metadata:
			  name: my-app
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("alias without an anchor is an error", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(kindPath, "Deployment")
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: *missing`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("wildcard path is an error", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(paths.Root().Child("items").IndexAll(), "x")
		doc := yamltest.FirstDocument(t, stringtest.Input(`items: [x]`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrWildcard)
	})
}

func TestContent_WithAll(t *testing.T) {
	t.Parallel()

	t.Run("multiple conditions all match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(apiVersionPath, "apps/v1"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: apps/v1
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("multiple conditions partial match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(apiVersionPath, "apps/v1"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: v1
		`))

		got := match(t, m, doc)
		assert.False(t, got)
	})
}

func TestContent_ContextEnded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

	// A matcher whose context ended cannot decide, so it returns the error
	// rather than a match, and the registry stops at the document.
	ok, err := matcher.Content(kindPath, "Deployment").Match(ctx, doc)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
}

// The error rejecting reports from its own decode.
var errRejecting = errors.New("value rejected itself")

// rejecting decodes itself and reports errRejecting, so the decoder
// returns the value's own error rather than a rejection of its own.
type rejecting struct{}

func (*rejecting) UnmarshalYAML([]byte) error {
	return errRejecting
}

func TestContent_UnmarshalerError(t *testing.T) {
	t.Parallel()

	// A value that rejects itself is not the decoder saying the value
	// does not read as T, so the matcher returns the error rather than
	// a no.
	m := matcher.Content(kindPath, rejecting{})
	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

	ok, err := m.Match(t.Context(), doc)
	require.ErrorIs(t, err, errRejecting)
	assert.False(t, ok)
}
