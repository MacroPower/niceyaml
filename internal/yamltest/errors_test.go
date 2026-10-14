package yamltest_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

func TestProblemsOf(t *testing.T) {
	t.Parallel()

	reason := errors.New("reason")
	badA := yamltest.ErrorAt("bad a", "a")
	badB := niceyaml.NewError("bad b", niceyaml.WithDetails(reason))

	tcs := map[string]struct {
		err         error
		wantProblem []error
		wantDetail  []error
	}{
		"nil":                        {},
		"one problem with no detail": {err: badA},
		"a summary heads its problems": {
			err:         niceyaml.NewSummary("2 problems", badA, badB),
			wantProblem: []error{badA, badB},
		},
		"a problem holds its details": {err: badB, wantDetail: []error{reason}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantProblem, yamltest.ErrsOf(yamltest.ProblemsOf(tc.err)))
			assert.Equal(t, tc.wantDetail, yamltest.ErrsOf(yamltest.DetailsOf(tc.err)))
		})
	}
}

func TestBoundIn(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"))
	bound := yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
		yamltest.ErrorAt("bad a", "a"),
		yamltest.ErrorAt("bad b", "b"),
	))

	t.Run("an error bound to no source holds no binding", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, yamltest.BoundIn(errors.New("plain")))
	})

	t.Run("a binding comes before the bindings below it", func(t *testing.T) {
		t.Parallel()

		var got []string

		for _, b := range yamltest.BoundIn(bound) {
			got = append(got, b.Message())
		}

		assert.Equal(t, []string{"2 problems", "bad a", "bad b"}, got)
	})

	t.Run("a binding the tree reaches twice comes once", func(t *testing.T) {
		t.Parallel()

		assert.Len(t, yamltest.BoundIn(errors.Join(bound, bound)), 3)
	})
}

func TestRequireSourceError(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	bound := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))

	t.Run("a wrapped binding comes back", func(t *testing.T) {
		t.Parallel()

		got := yamltest.RequireSourceError(t, fmt.Errorf("context: %w", bound))
		assert.Same(t, bound, got)
	})

	t.Run("an error with no binding fails the test", func(t *testing.T) {
		t.Parallel()

		var got *niceyaml.SourceError

		rec := record(func(tb *recordingTB) {
			got = yamltest.RequireSourceError(tb, errors.New("plain"))
		})

		assert.True(t, rec.failed)
		assert.Nil(t, got)
	})
}

func TestBoundNode(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a:", "  b: 1"))
	doc := yamltest.Document(t, source)
	scoped := yamltest.At(t, doc, paths.Doc().Child("a"))

	helpers := map[string]struct {
		get  func(testing.TB, *niceyaml.SourceError) *niceyaml.Node
		want *niceyaml.Node
	}{
		"BoundNode":     {get: yamltest.BoundNode, want: scoped},
		"BoundDocument": {get: yamltest.BoundDocument, want: doc},
	}

	for name, helper := range helpers {
		t.Run(name+" returns what the error is bound to", func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, scoped.Bind(errors.New("plain")))

			assert.Same(t, helper.want, helper.get(t, bound))
		})

		t.Run(name+" fails the test for an error bound to none", func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, source.Bind(errors.New("plain")))

			var got *niceyaml.Node

			rec := record(func(tb *recordingTB) {
				got = helper.get(tb, bound)
			})

			assert.True(t, rec.failed)
			assert.Nil(t, got)
		})
	}
}

func TestOnlyProblem(t *testing.T) {
	t.Parallel()

	badA := yamltest.ErrorAt("bad a", "a")
	badB := yamltest.ErrorAt("bad b", "b")

	tcs := map[string]struct {
		err  error
		want error // Nil when the helper fails the test.
	}{
		"one problem":                     {err: badA, want: badA},
		"a summary heads its one problem": {err: niceyaml.NewSummary("1 problem", badA), want: badA},
		"a join holds the summary": {
			err:  errors.Join(niceyaml.NewSummary("1 problem", badA)),
			want: badA,
		},
		"no problem":   {},
		"two problems": {err: niceyaml.NewSummary("2 problems", badA, badB)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got niceyaml.ErrorTree

			rec := record(func(tb *recordingTB) {
				got = yamltest.OnlyProblem(tb, tc.err)
			})

			assert.Equal(t, tc.want == nil, rec.failed, rec.msg)
			assert.Equal(t, tc.want, got.Err)
		})
	}
}

func TestRequireError(t *testing.T) {
	t.Parallel()

	located := yamltest.ErrorAt("bad a", "a")

	t.Run("a wrapped Error comes back", func(t *testing.T) {
		t.Parallel()

		assert.Same(t, located, yamltest.RequireError(t, fmt.Errorf("context: %w", located)))
	})

	t.Run("an error with no Error fails the test", func(t *testing.T) {
		t.Parallel()

		var got *niceyaml.Error

		rec := record(func(tb *recordingTB) {
			got = yamltest.RequireError(tb, errors.New("plain"))
		})

		assert.True(t, rec.failed)
		assert.Nil(t, got)
	})
}

func TestPathOf(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	located := yamltest.RequireSourceError(t, yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a")))
	unlocated := yamltest.RequireSourceError(t, source.Bind(errors.New("plain")))

	// Each helper reads one accessor of a binding. A binding with no
	// location reports none of them.
	helpers := map[string]struct {
		get  func(tb *recordingTB, bound *niceyaml.SourceError) any
		want any
	}{
		"PathOf": {
			get:  func(tb *recordingTB, bound *niceyaml.SourceError) any { return yamltest.PathOf(tb, bound).String() },
			want: "$.a",
		},
		"RangeOf": {
			get:  func(tb *recordingTB, bound *niceyaml.SourceError) any { return yamltest.RangeOf(tb, bound) },
			want: yamltest.LineRange(0, 3, 4),
		},
		"PositionOf": {
			get:  func(tb *recordingTB, bound *niceyaml.SourceError) any { return yamltest.PositionOf(tb, bound) },
			want: position.New(0, 3),
		},
	}

	for name, helper := range helpers {
		t.Run(name+" returns what the binding reports", func(t *testing.T) {
			t.Parallel()

			var got any

			rec := record(func(tb *recordingTB) {
				got = helper.get(tb, located)
			})

			assert.False(t, rec.failed, rec.msg)
			assert.Equal(t, helper.want, got)
		})

		t.Run(name+" fails the test for a binding that reports none", func(t *testing.T) {
			t.Parallel()

			rec := record(func(tb *recordingTB) {
				helper.get(tb, unlocated)
			})

			assert.True(t, rec.failed)
			assert.Contains(t, rec.msg, "*niceyaml.SourceError reports no")
		})
	}
}

func TestAssertBoundTo(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	other := niceyaml.NewSourceFromString("a: 1\n")
	bound := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))
	elsewhere := yamltest.Bind(t, other, yamltest.ErrorAt("bad a", "a"))

	tcs := map[string]struct {
		err   error
		fails bool
	}{
		"an error with no binding":       {err: errors.New("plain")},
		"every binding names the source": {err: errors.Join(bound, bound)},
		"a binding names another source": {err: elsewhere, fails: true},
		"one binding of two names another source": {
			err:   errors.Join(bound, elsewhere),
			fails: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := record(func(tb *recordingTB) {
				yamltest.AssertBoundTo(tb, source, tc.err)
			})

			assert.Equal(t, tc.fails, rec.failed, rec.msg)
		})
	}
}

func TestErrorAt(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a:", "  b: 1"))

	tcs := map[string]struct {
		path  string // The path the error carries.
		bound string // The text of its binding to source.
		keys  []string
	}{
		"no keys":     {path: "@", bound: "1:1: $: bad"},
		"one key":     {keys: []string{"a"}, path: "@.a", bound: "1:1: $.a: bad"},
		"nested keys": {keys: []string{"a", "b"}, path: "@.a.b", bound: "2:6: $.a.b: bad"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.ErrorAt("bad", tc.keys...)

			require.EqualError(t, err, "bad")
			assert.Equal(t, tc.path, yamltest.PathOf(t, err).String())
			require.EqualError(t, yamltest.Bind(t, source, err), tc.bound)
		})
	}
}

func TestWrapperErrors(t *testing.T) {
	t.Parallel()

	errPort := errors.New("bad port")
	errName := errors.New("bad name")

	tcs := map[string]struct {
		err  error
		want string
	}{
		"CheckError": {err: &yamltest.CheckError{Err: errPort}, want: "check: bad port"},
		"UpperError": {err: yamltest.UpperError{Err: errPort}, want: "BAD PORT"},
		"IndentError": {
			err:  yamltest.IndentError{Err: errors.Join(errPort, errName)},
			want: stringtest.JoinLF("validation:", "  bad port", "  bad name"),
		},
		"RewriteError": {err: yamltest.RewriteError{Err: errPort}, want: "rewritten"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.err, tc.want)
			require.ErrorIs(t, tc.err, errPort)
		})
	}

	t.Run("a *CheckError target finds a CheckError", func(t *testing.T) {
		t.Parallel()

		wrapped := &yamltest.CheckError{Err: errPort}

		var got *yamltest.CheckError

		require.ErrorAs(t, fmt.Errorf("context: %w", wrapped), &got)
		assert.Same(t, wrapped, got)
	})
}
