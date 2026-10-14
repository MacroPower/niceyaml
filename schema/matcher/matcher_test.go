package matcher_test

import (
	"context"
	"errors"
	"fmt"
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

// Compile-time interface satisfaction check, path helpers, and a matcher
// for tests.
var (
	_ matcher.Matcher = matcher.Func(nil)

	kindPath       = paths.Current().Child("kind")
	apiVersionPath = paths.Current().Child("apiVersion")
	metadataName   = paths.Current().Child("metadata").Child("name")
	missingPath    = paths.Current().Child("missing")
	versionPath    = paths.Current().Child("version")
	twoKeyPath     = paths.Current().Child("2").Key()
	enabledPath    = paths.Current().Child("enabled")
	timeoutPath    = paths.Current().Child("timeout")

	// The error undecided reports.
	errUndecided = errors.New("undecided")

	// A matcher that cannot decide, and reports errUndecided.
	undecided = matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) {
		return false, errUndecided
	})
)

// pointerMatcher is a [matcher.Matcher] with a pointer receiver, so a nil
// *pointerMatcher is a non-nil interface value holding a nil pointer.
type pointerMatcher struct{}

func (*pointerMatcher) Match(context.Context, *niceyaml.Node) (bool, error) {
	return true, nil
}

// recording returns a [matcher.Matcher] that matches every document and
// sets called when it runs.
func recording(called *bool) matcher.Matcher {
	return matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) {
		*called = true

		return true, nil
	})
}

// match runs m on doc and fails the test when the matcher cannot decide.
func match(t *testing.T, m matcher.Matcher, doc *niceyaml.Node) bool {
	t.Helper()

	got, err := m.Match(t.Context(), doc)
	require.NoError(t, err)

	return got
}

// testCombinator runs the checks that [matcher.All] and [matcher.Any]
// share on combine, whose panics name it as funcName.
func testCombinator(t *testing.T, funcName string, combine func(...matcher.Matcher) matcher.Matcher) {
	t.Helper()

	t.Run("error ends the evaluation", func(t *testing.T) {
		t.Parallel()

		called := false
		m := combine(undecided, recording(&called))
		doc := yamltest.FirstDocument(t, `kind: Deployment`)

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, errUndecided)
		assert.False(t, called)
	})

	t.Run("nil matcher panics", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			matchers []matcher.Matcher
			index    int
		}{
			"nil func first": {matchers: []matcher.Matcher{matcher.Func(nil)}, index: 0},
			"nil func after a matcher": {
				matchers: []matcher.Matcher{matcher.Exists(kindPath), matcher.Func(nil)},
				index:    1,
			},
			"nil interface first": {
				matchers: []matcher.Matcher{nil, matcher.Content(kindPath, "Test")},
				index:    0,
			},
			"nil interface after a matcher": {
				matchers: []matcher.Matcher{matcher.Content(kindPath, "Test"), nil},
				index:    1,
			},
			"nil pointer first": {
				matchers: []matcher.Matcher{(*pointerMatcher)(nil), matcher.Exists(kindPath)},
				index:    0,
			},
			"nil pointer after a matcher": {
				matchers: []matcher.Matcher{matcher.Exists(kindPath), (*pointerMatcher)(nil)},
				index:    1,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				want := fmt.Sprintf("%s: matcher at index %d is nil", funcName, tc.index)
				assert.PanicsWithValue(t, want, func() {
					combine(tc.matchers...)
				})
			})
		}
	})

	t.Run("writing to the caller's slice changes nothing", func(t *testing.T) {
		t.Parallel()

		ms := []matcher.Matcher{matcher.Content(kindPath, "Deployment")}
		m := combine(ms...)
		ms[0] = matcher.Content(kindPath, "Service")
		doc := yamltest.FirstDocument(t, `kind: Service`)

		assert.False(t, match(t, m, doc))
	})
}

// testCannotDecide checks that the matcher newMatcher creates for a path
// returns an error where it cannot decide, rather than a match or a plain
// no, so a registry stops at the document. Such a matcher cannot decide
// with a context that ended, or for a path that cannot resolve. A tag on
// an alias changes nothing.
func testCannotDecide(t *testing.T, newMatcher func(path paths.Path) matcher.Matcher) {
	t.Helper()

	t.Run("context that ended is an error", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		ok, err := newMatcher(kindPath).Match(ctx, yamltest.FirstDocument(t, `kind: Deployment`))
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, ok)
	})

	jobs := stringtest.Input(`
		jobs:
		  build:
		    steps:
		      - uses: checkout
	`)

	tcs := map[string]struct {
		err   error
		path  paths.Path
		input string
	}{
		"alias without an anchor":           {path: kindPath, input: `kind: *missing`, err: paths.ErrAlias},
		"tagged alias without an anchor":    {path: kindPath, input: `kind: !!str *missing`, err: paths.ErrAlias},
		"tagged alias to itself":            {path: kindPath, input: `kind: &a !!str *a`, err: paths.ErrAlias},
		"alias without an anchor on a path": {path: metadataName, input: `metadata: *missing`, err: paths.ErrAlias},
		"wildcard index": {
			path:  paths.Current().Child("items").IndexAll(),
			input: `items: [x]`,
			err:   paths.ErrWildcard,
		},
		"wildcard child": {path: paths.MustParse("$.jobs.*"), input: jobs, err: paths.ErrWildcard},
		"wildcard child before a key": {
			path:  paths.MustParse("$.jobs.*.steps"),
			input: jobs,
			err:   paths.ErrWildcard,
		},
		"wildcard index of a nested list": {
			path:  paths.MustParse("$.jobs.build.steps[*]"),
			input: jobs,
			err:   paths.ErrWildcard,
		},
		"recursive descent to a key":    {path: paths.MustParse("$..uses"), input: jobs, err: paths.ErrWildcard},
		"recursive descent to any node": {path: paths.MustParse("$..*"), input: jobs, err: paths.ErrWildcard},
	}

	t.Run("path that cannot resolve is an error", func(t *testing.T) {
		t.Parallel()

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				ok, err := newMatcher(tc.path).Match(t.Context(), doc)
				require.ErrorIs(t, err, tc.err)
				assert.False(t, ok)
			})
		}
	})
}

// testAliasBomb checks that m, a matcher of kind: Deployment, refuses a
// document whose aliases put a read of kind past the alias limit, unless
// [niceyaml.WithAliasLimit] turned the limit off for its source. Match
// refuses such a document before it decodes anything, as the schema
// validator does.
func testAliasBomb(t *testing.T, m matcher.Matcher) {
	t.Helper()

	// The tag keeps the alias in the node at kind, so Match counts the
	// document before it reads the node, and the levels put it past the
	// limit. With the limit off, kind reads as Deployment.
	taggedAliasBomb := stringtest.LinesLF("name: &name Deployment") +
		yamltest.AliasLevels(4) +
		stringtest.LinesLF("kind: !!str *name")

	tcs := map[string]struct {
		err   error
		input string
		opts  []niceyaml.SourceOption
		want  bool
	}{
		"alias key is an error": {
			// Each level lists the level below ten times, so a decode of
			// the alias key at kind reads 10^7 scalars.
			input: yamltest.AliasLevels(7) + yamltest.AliasKey("kind", 7),
			err:   schema.ErrExcessiveAliasing,
		},
		"tagged alias is an error": {input: taggedAliasBomb, err: schema.ErrExcessiveAliasing},
		"tagged alias in a source with the limit off is read": {
			input: taggedAliasBomb,
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
			want:  true,
		},
	}

	t.Run("alias bomb", func(t *testing.T) {
		t.Parallel()

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, tc.opts...)

				ok, err := m.Match(t.Context(), doc)
				assert.Equal(t, tc.want, ok)

				if tc.err == nil {
					require.NoError(t, err)

					return
				}

				require.ErrorIs(t, err, tc.err)

				// The aliases of the document are the cause, so the
				// document is at fault for the refusal.
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})
}

func TestFunc(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		fn   matcher.Func
		err  error
		want bool
	}{
		"reports the result of the function": {
			fn:   func(context.Context, *niceyaml.Node) (bool, error) { return true, nil },
			want: true,
		},
		"passes the error of the function through": {fn: undecided, err: errUndecided},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, "kind: Test")

			got, err := tc.fn.Match(t.Context(), doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFunc_Null(t *testing.T) {
	t.Parallel()

	// The function the doc of matcher.Func shows, which matches a null.
	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Node) (bool, error) {
		var value any

		found, err := doc.DecodeIfPresent(ctx, enabledPath, &value)
		if errors.Is(err, niceyaml.ErrDecode) {
			return false, nil
		}

		if err != nil {
			//nolint:wrapcheck // The Node binds the error already.
			return false, err
		}

		return found && value == nil, nil
	})

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"null":                      {input: `enabled: null`, want: true},
		"tilde":                     {input: `enabled: ~`, want: true},
		"no value":                  {input: `enabled:`, want: true},
		"alias to a null":           {input: "n: &n null\nenabled: *n", want: true},
		"false":                     {input: `enabled: false`},
		"empty string":              {input: `enabled: ""`},
		"str-tagged null":           {input: `enabled: !!str null`},
		"empty mapping":             {input: `enabled: {}`},
		"missing path":              {input: `kind: Test`},
		"value the decoder rejects": {input: `enabled: !!int x`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			assert.Equal(t, tc.want, match(t, m, doc))
		})
	}
}
