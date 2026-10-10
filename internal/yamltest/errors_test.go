package yamltest_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestProblemsOf(t *testing.T) {
	t.Parallel()

	reason := errors.New("reason")
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
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
		"a problem holds its details": {
			err:        badB,
			wantDetail: []error{reason},
		},
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

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	bound := yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
		niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))),
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
	bound := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))

	t.Run("a wrapped binding comes back", func(t *testing.T) {
		t.Parallel()

		got := yamltest.RequireSourceError(t, fmt.Errorf("context: %w", bound))
		assert.Same(t, bound, got)
	})

	t.Run("an error with no binding fails the test", func(t *testing.T) {
		t.Parallel()

		var got *niceyaml.SourceError

		rec := &recordingTB{}
		done := make(chan struct{})

		// FailNow ends the goroutine it runs on, so the helper runs on its own.
		go func() {
			defer close(done)

			got = yamltest.RequireSourceError(rec, errors.New("plain"))
		}()

		<-done

		assert.True(t, rec.failed)
		assert.Nil(t, got)
	})
}
