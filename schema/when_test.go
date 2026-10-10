package schema_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// pointerMatcher is a [matcher.Matcher] with a pointer receiver, so a nil
// *pointerMatcher is a non-nil interface value holding a nil pointer.
type pointerMatcher struct{}

func (*pointerMatcher) Match(context.Context, *niceyaml.Node) (bool, error) {
	return true, nil
}

func TestWhen(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)

	tcs := map[string]struct {
		input   string
		wantErr error
	}{
		"matcher accepts": {
			input: `kind: Deployment`,
		},
		"matcher rejects": {
			input:   `kind: Service`,
			wantErr: schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := schema.When(
				matcher.Content(kindPath, "Deployment"),
				schema.Embedded(schemaData),
			)

			doc := yamltest.FirstDocument(t, tc.input)
			ref, err := r.Resolve(t.Context(), doc)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, ref.Key())

			data, err := schema.NewRegistry().Load(t.Context(), ref)
			require.NoError(t, err)
			assert.Equal(t, schemaData, data)
		})
	}

	t.Run("guarded resolver errors pass through", func(t *testing.T) {
		t.Parallel()

		inner := errors.New("inner")
		r := schema.When(matcher.Content(kindPath, "Deployment"), failing(inner))

		doc := yamltest.FirstDocument(t, "kind: Deployment")
		_, err := r.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, inner)
	})

	t.Run("matcher errors pass through", func(t *testing.T) {
		t.Parallel()

		undecided := errors.New("undecided")
		called := false
		r := schema.When(
			matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
				return false, undecided
			}),
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				called = true

				return schema.Ref{}, nil
			}),
		)

		doc := yamltest.FirstDocument(t, "kind: Deployment")
		_, err := r.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, undecided)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
		assert.False(t, called)
	})

	t.Run("alias bomb stops the registry", func(t *testing.T) {
		t.Parallel()

		// The matcher runs before any schema, so it refuses a document
		// whose aliases would make its decode read 10^7 scalars, and the
		// registry stops at the document.
		input := yamltest.AliasLevels(7) + "kind:\n  ? *l7\n  : v\n"

		tcs := map[string]struct {
			matcher matcher.Matcher
		}{
			"content matcher": {
				matcher: matcher.Content(kindPath, "Deployment"),
			},
			"text matcher": {
				matcher: matcher.Text(kindPath, func(string) bool { return true }),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(schema.WithResolvers(schema.When(
					tc.matcher,
					schema.Embedded(schemaData),
				)))

				doc := yamltest.FirstDocumentWithPath(t, input, "app.yaml")
				err := reg.Check(t.Context(), doc)
				require.EqualError(t, err, "resolve schema: app.yaml: excessive aliasing")
				require.ErrorIs(t, err, schema.ErrResolve)
				require.ErrorIs(t, err, schema.ErrExcessiveAliasing)

				// The aliases of the document are the cause, so the
				// document is at fault for the error, as it is for a
				// schema's refusal.
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("guarded resolver is not consulted on reject", func(t *testing.T) {
		t.Parallel()

		called := false
		r := schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				called = true

				return schema.Ref{}, nil
			}),
		)

		doc := yamltest.FirstDocument(t, "kind: Service")
		_, err := r.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		assert.False(t, called)
	})

	t.Run("nil argument panics", func(t *testing.T) {
		t.Parallel()

		const (
			noMatcher  = "schema.When: matcher is nil"
			noResolver = "schema.When: resolver is nil"
		)

		byKind := matcher.Content(kindPath, "x")
		embedded := schema.Embedded(schemaData)

		tcs := map[string]struct {
			matcher  matcher.Matcher
			resolver schema.Resolver
			want     string
		}{
			"nil matcher":          {matcher: nil, resolver: embedded, want: noMatcher},
			"nil Func matcher":     {matcher: matcher.Func(nil), resolver: embedded, want: noMatcher},
			"nil pointer matcher":  {matcher: (*pointerMatcher)(nil), resolver: embedded, want: noMatcher},
			"nil resolver":         {matcher: byKind, resolver: nil, want: noResolver},
			"nil ResolverFunc":     {matcher: byKind, resolver: schema.ResolverFunc(nil), want: noResolver},
			"nil pointer resolver": {matcher: byKind, resolver: (*schema.Schema)(nil), want: noResolver},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, tc.want, func() {
					schema.When(tc.matcher, tc.resolver)
				})
			})
		}
	})
}
