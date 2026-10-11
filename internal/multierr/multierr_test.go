package multierr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/multierr"
)

// listError holds several errors under a message of its own type.
type listError struct {
	msg  string
	errs []error
}

func (e *listError) Error() string   { return e.msg }
func (e *listError) Unwrap() []error { return e.errs }

var (
	errA = errors.New("a")
	errB = errors.New("b")
	errC = errors.New("c")
)

func TestJoin(t *testing.T) {
	t.Parallel()

	require.NoError(t, multierr.Join(nil))
	assert.Same(t, errA, multierr.Join([]error{errA}))

	joined := multierr.Join([]error{errA, errB})
	assert.True(t, multierr.IsJoin(joined))
	assert.EqualError(t, joined, "a\nb")
}

func TestIsJoin(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err       error
		join      bool
		wrapError bool
	}{
		"join": {
			err:  errors.Join(errA, errB),
			join: true,
		},
		"several verbs": {
			err:       fmt.Errorf("%w: %w", errA, errB),
			wrapError: true,
		},
		"one verb": {
			err: fmt.Errorf("x: %w", errA),
		},
		"other type": {
			err: &listError{msg: "a\nb", errs: []error{errA, errB}},
		},
		"nil": {
			err: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.join, multierr.IsJoin(tc.err))
			assert.Equal(t, tc.wrapError, multierr.IsWrapErrors(tc.err))
		})
	}
}

func TestJoinMessage(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		msg      string
		branches []error
		want     bool
	}{
		"one per line": {
			msg:      "a\nb",
			branches: []error{errA, errB},
			want:     true,
		},
		"nil branch takes no line": {
			msg:      "a\nb",
			branches: []error{errA, nil, errB},
			want:     true,
		},
		"no branches": {
			msg:  "",
			want: true,
		},
		"other separator": {
			msg:      "a; b",
			branches: []error{errA, errB},
		},
		"text after the branches": {
			msg:      "a\nb\n",
			branches: []error{errA, errB},
		},
		"other order": {
			msg:      "b\na",
			branches: []error{errA, errB},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, multierr.JoinMessage(tc.msg, tc.branches))
		})
	}
}

func TestFirst(t *testing.T) {
	t.Parallel()

	assert.Same(t, errB, multierr.First([]error{nil, errB, errC}))
	require.NoError(t, multierr.First([]error{nil}))
	require.NoError(t, multierr.First(nil))
}

func TestHoldsFirst(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		msg      string
		branches []error
		want     bool
	}{
		"holds the first": {
			msg:      "x: a, b",
			branches: []error{nil, errA, errB},
			want:     true,
		},
		"holds a later one alone": {
			msg:      "x: b",
			branches: []error{errA, errB},
		},
		"no branches": {
			msg: "x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, multierr.HoldsFirst(tc.msg, tc.branches))
		})
	}
}

func TestRejoin(t *testing.T) {
	t.Parallel()

	t.Run("join", func(t *testing.T) {
		t.Parallel()

		got := multierr.Rejoin(errors.Join(errA, errB), []error{errC, errB})

		assert.True(t, multierr.IsJoin(got))
		require.EqualError(t, got, "c\nb")
		require.ErrorIs(t, got, errC)
	})

	t.Run("join of another type", func(t *testing.T) {
		t.Parallel()

		joined := &listError{msg: "a\nb", errs: []error{errA, errB}}

		got := multierr.Rejoin(joined, []error{errC})

		assert.False(t, multierr.IsJoin(got))
		require.EqualError(t, got, "c")
		require.ErrorIs(t, got, errC)
		require.ErrorIs(t, got, joined)

		// The stand-in matches the join itself, not what the join wraps.
		require.NotErrorIs(t, got, errA)

		var list *listError

		require.ErrorAs(t, got, &list)
		assert.Same(t, joined, list)
	})
}

// matchError matches errA for errors.Is, and wraps nothing.
type matchError struct{}

func (matchError) Error() string        { return "match" }
func (matchError) Is(target error) bool { return target == errA }

func TestReplace(t *testing.T) {
	t.Parallel()

	wrapper := fmt.Errorf("x: %w", errA)

	got := multierr.Replace(wrapper, errC, "x: c", []error{errB})

	require.EqualError(t, got, "x: c")
	assert.Same(t, errC, errors.Unwrap(got))
	require.ErrorIs(t, got, wrapper)
	require.ErrorIs(t, got, errB)
	require.ErrorIs(t, got, errC)

	// The stand-in matches the wrapper itself, not what it wraps.
	require.NotErrorIs(t, got, errA)

	t.Run("wrapper with an Is method", func(t *testing.T) {
		t.Parallel()

		got := multierr.Replace(matchError{}, errC, "match", nil)

		require.ErrorIs(t, got, errA)

		var m matchError

		require.ErrorAs(t, got, &m)
	})
}
