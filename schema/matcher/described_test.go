package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestDescribed(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		err     error
		want    bool
	}{
		"matches what the matcher matches": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   "kind: Deployment",
			want:    true,
		},
		"declines what the matcher declines": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   "kind: Service",
		},
		"passes the error of the matcher through": {
			matcher: undecided,
			input:   "kind: Deployment",
			err:     errUndecided,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.Described(tc.matcher, "the document is a deployment")
			assert.Equal(t, "the document is a deployment", description(t, m))

			got, err := m.Match(t.Context(), yamltest.FirstDocument(t, tc.input))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("replaces the description of a matcher of the package", func(t *testing.T) {
		t.Parallel()

		m := matcher.Described(matcher.MustFilePath("/srv/tenants/*/menu.yaml"), "the file is a menu")
		assert.Equal(t, "the file is a menu", description(t, m))
	})

	t.Run("panics on a nil matcher or an empty description", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			matcher     matcher.Matcher
			description string
			want        string
		}{
			"nil matcher": {description: "x", want: "matcher.Described: matcher is nil"},
			"nil Func": {
				matcher:     matcher.Func(nil),
				description: "x",
				want:        "matcher.Described: matcher is nil",
			},
			"nil pointer": {
				matcher:     (*pointerMatcher)(nil),
				description: "x",
				want:        "matcher.Described: matcher is nil",
			},
			"empty description": {matcher: matcher.Exists(kindPath), want: "matcher.Described: description is empty"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, tc.want, func() {
					matcher.Described(tc.matcher, tc.description)
				})
			})
		}
	})
}
