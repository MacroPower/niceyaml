package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/matcher"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNot(t *testing.T) {
	t.Parallel()

	notService := matcher.Not(matcher.Content(kindPath, "Service"))

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		err     error
		want    bool
	}{
		"declines what the matcher matches": {
			matcher: notService,
			input:   `kind: Service`,
		},
		"matches a different value": {
			matcher: notService,
			input:   `kind: Deployment`,
			want:    true,
		},
		"matches a document without the path": {
			matcher: notService,
			input:   `name: app`,
			want:    true,
		},
		"matches a node that is not a scalar": {
			matcher: notService,
			input:   `kind: {name: Service}`,
			want:    true,
		},
		"matches the absence of a field": {
			matcher: matcher.Not(matcher.Exists(kindPath)),
			input:   `name: app`,
			want:    true,
		},
		"declines the presence of a field": {
			matcher: matcher.Not(matcher.Exists(kindPath)),
			input:   `kind: Service`,
		},
		"paired with Exists declines a document without the path": {
			matcher: matcher.All(matcher.Exists(kindPath), notService),
			input:   `name: app`,
		},
		"a Not of a Not matches what the matcher matches": {
			matcher: matcher.Not(notService),
			input:   `kind: Service`,
			want:    true,
		},
		"passes the error of the matcher through": {
			matcher: matcher.Not(undecided),
			input:   `kind: Service`,
			err:     errUndecided,
		},
		"passes the error of a path that cannot resolve through": {
			matcher: notService,
			input:   `kind: *missing`,
			err:     paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.matcher.Match(t.Context(), yamltest.FirstDocument(t, tc.input))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("excludes a file path pattern", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.MustFilePath("k8s/**", matcher.WithBaseDir("/srv")),
			matcher.Not(matcher.MustFilePath("k8s/vendor/**", matcher.WithBaseDir("/srv"))),
		)

		tcs := map[string]struct {
			path string
			want bool
		}{
			"file outside the excluded directory": {path: "/srv/k8s/app.yaml", want: true},
			"file in the excluded directory":      {path: "/srv/k8s/vendor/app.yaml"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, `kind: Service`, tc.path)

				assert.Equal(t, tc.want, match(t, m, doc))
			})
		}
	})

	t.Run("panics on a nil matcher", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			matcher matcher.Matcher
		}{
			"nil matcher": {},
			"nil Func":    {matcher: matcher.Func(nil)},
			"nil pointer": {matcher: (*pointerMatcher)(nil)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, "matcher.Not: matcher is nil", func() {
					matcher.Not(tc.matcher)
				})
			})
		}
	})
}

func TestNot_Describe(t *testing.T) {
	t.Parallel()

	var (
		kind       = paths.Doc().Child("kind")
		deployment = matcher.Content(kind, "Deployment")
		service    = matcher.Content(kind, "Service")
		vendored   = matcher.MustFilePath("k8s/vendor/**")
	)

	tcs := map[string]struct {
		matcher matcher.Matcher
		want    string
	}{
		"one matcher": {
			matcher: matcher.Not(matcher.Exists(kind)),
			want:    `not ($.kind exists)`,
		},
		"an Any of two": {
			matcher: matcher.Not(matcher.Any(deployment, service)),
			want:    `not ($.kind is "Deployment" or $.kind is "Service")`,
		},
		"a Not of a Not": {
			matcher: matcher.Not(matcher.Not(deployment)),
			want:    `not (not ($.kind is "Deployment"))`,
		},
		"a Not beside another matcher in an All": {
			matcher: matcher.All(deployment, matcher.Not(vendored)),
			want:    `$.kind is "Deployment" and not (file path matches "k8s/vendor/**")`,
		},
		"a described matcher keeps its own words": {
			matcher: matcher.Not(matcher.Described(undecided, "the file is vendored")),
			want:    `not (the file is vendored)`,
		},
		"a Func has no description": {
			matcher: matcher.Not(undecided),
		},
		"an All with a Not of a Func has no description": {
			matcher: matcher.All(deployment, matcher.Not(undecided)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, description(t, tc.matcher))
		})
	}
}
