package schema_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
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
	byKind := matcher.Content(kindPath, "Deployment")
	inner := errors.New("inner")
	undecided := errors.New("undecided")

	// Each case guards resolver with matcher. The err is what Resolve
	// reports, or nil for a resolve that names the schema of resolver.
	tcs := map[string]struct {
		matcher  matcher.Matcher
		resolver schema.Resolver
		err      error
		input    string
		// The guarded resolver runs.
		wantCalled bool
	}{
		"matcher accepts": {
			matcher:    byKind,
			resolver:   schema.Embedded(schemaData),
			input:      "kind: Deployment",
			wantCalled: true,
		},
		"guarded resolver errors pass through": {
			matcher:    byKind,
			resolver:   failing(inner),
			input:      "kind: Deployment",
			err:        inner,
			wantCalled: true,
		},
		"matcher errors pass through": {
			matcher: matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) {
				return false, undecided
			}),
			resolver: schema.Embedded(schemaData),
			input:    "kind: Deployment",
			err:      undecided,
		},
		"matcher rejects without consulting the guarded resolver": {
			matcher:  byKind,
			resolver: schema.Embedded(schemaData),
			input:    "kind: Service",
			err:      schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			called := false
			r := schema.When(
				tc.matcher,
				schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
					called = true

					return tc.resolver.Resolve(ctx, doc)
				}),
			)

			doc := yamltest.FirstDocument(t, tc.input)

			if tc.err != nil {
				_, err := r.Resolve(t.Context(), doc)
				require.ErrorIs(t, err, tc.err)
				// Only a matcher that rejects the document reports ErrNoMatch.
				assert.Equal(t, errors.Is(tc.err, schema.ErrNoMatch), errors.Is(err, schema.ErrNoMatch))
				assert.Equal(t, tc.wantCalled, called)

				return
			}

			key, data, err := loadFor(t, r, doc)
			require.NoError(t, err)
			assert.NotEmpty(t, key)
			assert.Equal(t, schemaData, data)
			assert.Equal(t, tc.wantCalled, called)
		})
	}

	t.Run("a decline gives the description of the matcher", func(t *testing.T) {
		t.Parallel()

		deployment := matcher.Content(paths.Doc().Child("kind"), "Deployment")
		service := matcher.Content(paths.Doc().Child("kind"), "Service")
		isPod := matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) { return false, nil })

		tcs := map[string]struct {
			matcher matcher.Matcher
			want    string
		}{
			"content guard": {
				matcher: deployment,
				want:    `no matching schema: applies only when $.kind is "Deployment"`,
			},
			"file path guard": {
				matcher: matcher.MustFilePath("menus/*.{yaml,yml}"),
				want:    `no matching schema: applies only when file path matches "menus/*.{yaml,yml}"`,
			},
			"all guard": {
				matcher: matcher.All(matcher.MustFilePath("k8s/*.yaml"), deployment),
				want:    `no matching schema: applies only when file path matches "k8s/*.yaml" and $.kind is "Deployment"`,
			},
			"any guard": {
				matcher: matcher.Any(deployment, service),
				want:    `no matching schema: applies only when $.kind is "Deployment" or $.kind is "Service"`,
			},
			"described guard": {
				matcher: matcher.Described(isPod, "the file holds a pod"),
				want:    "no matching schema: applies only when the file holds a pod",
			},
			// A Func does not describe itself, so it declines with
			// ErrNoMatch alone.
			"func guard": {
				matcher: isPod,
				want:    "no matching schema",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				r := schema.When(tc.matcher, schema.Embedded(schemaData))
				doc := yamltest.FirstDocumentWithPath(t, "kind: ConfigMap", "other/app.yaml")

				_, err := r.Resolve(t.Context(), doc)
				require.ErrorIs(t, err, schema.ErrNoMatch)
				require.EqualError(t, err, tc.want)
			})
		}
	})

	t.Run("alias bomb stops the registry", func(t *testing.T) {
		t.Parallel()

		// The matcher runs before any schema, so it refuses a document
		// whose aliases would make its decode read 10^7 scalars, and the
		// registry stops at the document.
		input := yamltest.AliasLevels(7) + yamltest.AliasKey("kind", 7)

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
				require.EqualError(
					t,
					err,
					"resolve schema: app.yaml:11:5: aliases expand the document past the alias limit",
				)
				require.ErrorIs(t, err, schema.ErrResolve)
				require.ErrorIs(t, err, schema.ErrExcessiveAliasing)

				// The aliases of the document are the cause, so the
				// document is at fault for the error, as it is for a
				// schema's refusal.
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("nil argument panics", func(t *testing.T) {
		t.Parallel()

		const (
			noMatcher  = "schema.When: matcher is nil"
			noResolver = "schema.When: resolver is nil"
		)

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
