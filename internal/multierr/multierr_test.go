package multierr_test

import (
	"errors"
	"fmt"
	"reflect"
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

// asError holds several errors, and its As method sets a target of type
// [matchError], which it holds nowhere.
type asError struct {
	listError
}

func (*asError) As(target any) bool {
	m, ok := target.(*matchError)
	if ok {
		*m = matchError{}
	}

	return ok
}

// matchError matches errA for errors.Is, and wraps nothing.
type matchError struct{}

func (matchError) Error() string        { return "match" }
func (matchError) Is(target error) bool { return target == errA }

var (
	errA = errors.New("a")
	errB = errors.New("b")
	errC = errors.New("c")
)

// requireAs requires errors.As to find an error of the type of want in
// err, and to set the target to want itself.
func requireAs(tb testing.TB, err, want error) {
	tb.Helper()

	target := reflect.New(reflect.TypeOf(want))
	require.ErrorAs(tb, err, target.Interface())
	assert.True(tb, target.Elem().Equal(reflect.ValueOf(want)), "errors.As set %v", target.Elem())
}

func TestJoin(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		errs []error
		// The message of the error Join returns, which is nil when want is
		// empty.
		want string
		// Whether Join returns the first error itself.
		same bool
		join bool
	}{
		"no errors":      {},
		"one error":      {errs: []error{errA}, want: "a", same: true},
		"several errors": {errs: []error{errA, errB}, want: "a\nb", join: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.Join(tc.errs)
			if tc.want == "" {
				require.NoError(t, got)

				return
			}

			require.EqualError(t, got, tc.want)
			assert.Equal(t, tc.join, multierr.IsJoin(got))

			if tc.same {
				assert.Same(t, tc.errs[0], got)
			}
		})
	}
}

func TestIsJoin(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err       error
		join      bool
		wrapError bool
		wrap      bool
		built     bool
	}{
		"join": {err: errors.Join(errA, errB), join: true},
		"stand-in for a join": {
			err:   multierr.Rejoin(&listError{msg: "a\nb", errs: []error{errA, errB}}, []error{errC}),
			built: true,
		},
		"several verbs": {err: fmt.Errorf("%w: %w", errA, errB), wrapError: true},
		"stand-in for several verbs": {
			err:       multierr.Rewrap(fmt.Errorf("%w: %w", errA, errB), []error{errA, errC}, "a: c"),
			wrapError: true,
			built:     true,
		},
		"rebuilt several verbs": {
			err:       multierr.WrapErrors([]error{errA, errC}, "a: c"),
			wrapError: true,
			built:     true,
		},
		"one verb": {err: fmt.Errorf("x: %w", errA), wrap: true},
		"stand-in for one verb": {
			err:   multierr.Replace(fmt.Errorf("x: %w", errA), errC, "x: c", nil),
			built: true,
		},
		"rebuilt one verb": {err: multierr.Wrap(errC, "x: c"), built: true},
		"other type":       {err: &listError{msg: "a\nb", errs: []error{errA, errB}}},
		"nil":              {err: nil},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.join, multierr.IsJoin(tc.err))
			assert.Equal(t, tc.wrapError, multierr.IsWrapErrors(tc.err))
			assert.Equal(t, tc.wrap, multierr.IsWrap(tc.err))
			assert.Equal(t, tc.built, multierr.IsBuilt(tc.err))
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
		"one per line":             {msg: "a\nb", branches: []error{errA, errB}, want: true},
		"nil branch takes no line": {msg: "a\nb", branches: []error{errA, nil, errB}, want: true},
		"no branches":              {msg: "", want: true},
		"other separator":          {msg: "a; b", branches: []error{errA, errB}},
		"text after the branches":  {msg: "a\nb\n", branches: []error{errA, errB}},
		"other order":              {msg: "b\na", branches: []error{errA, errB}},
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

	tcs := map[string]struct {
		want     error
		branches []error
	}{
		"past nil branches":  {branches: []error{nil, errB, errC}, want: errB},
		"nil branches alone": {branches: []error{nil}},
		"no branches":        {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.First(tc.branches)
			if tc.want == nil {
				require.NoError(t, got)

				return
			}

			assert.Same(t, tc.want, got)
		})
	}
}

func TestHoldsFirst(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		msg      string
		branches []error
		want     bool
	}{
		"holds the first":         {msg: "x: a, b", branches: []error{nil, errA, errB}, want: true},
		"holds a later one alone": {msg: "x: b", branches: []error{errA, errB}},
		"no branches":             {msg: "x"},
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

	list := &listError{msg: "a\nb", errs: []error{errA, errB}}

	tcs := map[string]struct {
		joined error
		// The error errors.As finds in the result for a target of its
		// type, or nil.
		as       error
		branches []error
		// The errors the result matches for errors.Is, and those it does
		// not.
		is, isNot []error
		want      string
		join      bool
	}{
		"join": {
			joined:   errors.Join(errA, errB),
			branches: []error{errC, errB},
			want:     "c\nb",
			join:     true,
			is:       []error{errC},
		},
		"join of another type": {
			joined:   list,
			branches: []error{errC},
			want:     "c",
			// The stand-in matches the join itself, not what the join
			// wraps.
			is:    []error{errC, list},
			isNot: []error{errA},
			as:    list,
		},
		"join with an As method": {
			joined:   &asError{listError{msg: "a\nb", errs: []error{errA, errB}}},
			branches: []error{errC},
			want:     "c",
			is:       []error{errC},
			// The As method of the join sets the target.
			as: matchError{},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.Rejoin(tc.joined, tc.branches)

			assert.Equal(t, tc.join, multierr.IsJoin(got))
			require.EqualError(t, got, tc.want)

			for _, err := range tc.is {
				require.ErrorIs(t, got, err)
			}

			for _, err := range tc.isNot {
				require.NotErrorIs(t, got, err)
			}

			if tc.as != nil {
				requireAs(t, got, tc.as)
			}
		})
	}
}

func TestRewrap(t *testing.T) {
	t.Parallel()

	wrapper := fmt.Errorf("%w: %w", errA, errB)

	tcs := map[string]struct {
		wrapper error
		// The error errors.As finds in the result for a target of its
		// type, or nil.
		as       error
		msg      string
		branches []error
		// The errors the result matches for errors.Is, and those it does
		// not.
		is, isNot []error
	}{
		"wrapper": {
			wrapper:  wrapper,
			msg:      "a: c",
			branches: []error{errA, errC},
			// The stand-in matches the wrapper itself and its own
			// branches, not a branch it replaced.
			is:    []error{wrapper, errA, errC},
			isNot: []error{errB},
		},
		"wrapper with an Is method": {
			wrapper:  matchError{},
			msg:      "match: c",
			branches: []error{errC},
			is:       []error{errA, errC},
			as:       matchError{},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.Rewrap(tc.wrapper, tc.branches, tc.msg)

			require.EqualError(t, got, tc.msg)
			assert.True(t, multierr.IsWrapErrors(got))

			unwrapped, ok := got.(interface{ Unwrap() []error })
			require.True(t, ok)
			assert.Equal(t, tc.branches, unwrapped.Unwrap())

			for _, err := range tc.is {
				require.ErrorIs(t, got, err)
			}

			for _, err := range tc.isNot {
				require.NotErrorIs(t, got, err)
			}

			if tc.as != nil {
				requireAs(t, got, tc.as)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	t.Parallel()

	wrapper := fmt.Errorf("x: %w", errA)
	list := &listError{msg: "a\nb", errs: []error{errA, errB}}

	tcs := map[string]struct {
		wrapper error
		err     error
		// The error errors.As finds in the result for a target of its
		// type, or nil.
		as     error
		msg    string
		beside []error
		// The errors the result matches for errors.Is, and those it does
		// not.
		is, isNot []error
	}{
		"wrapper": {
			wrapper: wrapper,
			err:     errC,
			msg:     "x: c",
			beside:  []error{errB},
			is:      []error{wrapper, errB, errC},
			// The stand-in matches the wrapper itself, not what it wraps.
			isNot: []error{errA},
		},
		"wrapper with an Is method": {
			wrapper: matchError{},
			err:     errC,
			msg:     "match",
			is:      []error{errA},
			as:      matchError{},
		},
		"an error beside the wrapper sets the target": {
			wrapper: wrapper,
			err:     errC,
			msg:     "x: c",
			beside:  []error{list},
			as:      list,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.Replace(tc.wrapper, tc.err, tc.msg, tc.beside)

			require.EqualError(t, got, tc.msg)
			assert.Same(t, tc.err, errors.Unwrap(got))

			for _, err := range tc.is {
				require.ErrorIs(t, got, err)
			}

			for _, err := range tc.isNot {
				require.NotErrorIs(t, got, err)
			}

			if tc.as != nil {
				requireAs(t, got, tc.as)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err error
		msg string
		// The errors the result matches for errors.Is, and those it does
		// not.
		is, isNot []error
	}{
		"error": {
			err:   errC,
			msg:   "x: c",
			is:    []error{errC},
			isNot: []error{errA},
		},
		"error with an Is method": {
			err: matchError{},
			msg: "x: match",
			is:  []error{errA},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.Wrap(tc.err, tc.msg)

			require.EqualError(t, got, tc.msg)
			assert.Equal(t, tc.err, errors.Unwrap(got))

			for _, err := range tc.is {
				require.ErrorIs(t, got, err)
			}

			for _, err := range tc.isNot {
				require.NotErrorIs(t, got, err)
			}

			// The result matches only through what it wraps, so a target
			// of no type the chain holds stays unset.
			var list *listError

			assert.NotErrorAs(t, got, &list)
		})
	}
}

func TestWrapErrors(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		msg      string
		branches []error
		// The errors the result matches for errors.Is, and those it does
		// not.
		is, isNot []error
	}{
		"branches": {
			msg:      "a: c",
			branches: []error{errA, errC},
			is:       []error{errA, errC},
			isNot:    []error{errB},
		},
		"branch with an Is method": {
			msg:      "match: c",
			branches: []error{matchError{}, errC},
			is:       []error{errA, errC},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := multierr.WrapErrors(tc.branches, tc.msg)

			require.EqualError(t, got, tc.msg)
			assert.True(t, multierr.IsWrapErrors(got))

			unwrapped, ok := got.(interface{ Unwrap() []error })
			require.True(t, ok)
			assert.Equal(t, tc.branches, unwrapped.Unwrap())

			for _, err := range tc.is {
				require.ErrorIs(t, got, err)
			}

			for _, err := range tc.isNot {
				require.NotErrorIs(t, got, err)
			}

			var list *listError

			assert.NotErrorAs(t, got, &list)
		})
	}
}

func TestMap(t *testing.T) {
	t.Parallel()

	list := &listError{msg: "a\nb", errs: []error{errA, errB}}
	wrapper := fmt.Errorf("x: %w", errA)

	tcs := map[string]struct {
		err error
		// The error errors.As finds in the result for a target of its
		// type, or nil.
		as error
		// The errors the result unwraps to.
		unwraps []error
		// The errors the result matches for errors.Is, and those it does
		// not.
		is, isNot []error
		want      string
		changed   bool
	}{
		"branch of a stand-in for a join": {
			err:     multierr.Rejoin(list, []error{errA, errB}),
			want:    "c\nb",
			changed: true,
			unwraps: []error{errC, errB},
			// The copy keeps the join it stands in for, which f keeps.
			is:    []error{list, errC},
			isNot: []error{errA},
			as:    list,
		},
		"join a stand-in stands in for": {
			err:     multierr.Rejoin(errA, []error{errB}),
			want:    "b",
			changed: true,
			unwraps: []error{errB},
			is:      []error{errC, errB},
			isNot:   []error{errA},
		},
		"branch of a stand-in for several verbs": {
			err:     multierr.Rewrap(fmt.Errorf("%w: %w", errA, errB), []error{errA, errB}, "a: b"),
			want:    "a: b",
			changed: true,
			unwraps: []error{errC, errB},
			is:      []error{errC, errB},
			isNot:   []error{errA},
		},
		"nil branch": {
			err:     multierr.WrapErrors([]error{nil, errA}, "a"),
			want:    "a",
			changed: true,
			unwraps: []error{nil, errC},
			is:      []error{errC},
		},
		"error a stand-in for one verb wraps": {
			err:     multierr.Replace(wrapper, errA, "x: a", nil),
			want:    "x: a",
			changed: true,
			unwraps: []error{errC},
			is:      []error{wrapper, errC},
		},
		"error beside a stand-in": {
			err:     multierr.Replace(wrapper, errB, "x: b", []error{errA}),
			want:    "x: b",
			changed: true,
			unwraps: []error{errB},
			is:      []error{errC, errB},
			isNot:   []error{errA},
		},
		"error that f does not change": {
			err:     multierr.Replace(wrapper, errB, "x: b", []error{errB}),
			want:    "x: b",
			unwraps: []error{errB},
		},
		"error this package did not build": {
			err:     wrapper,
			want:    "x: a",
			unwraps: []error{errA},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The function replaces errA with errC.
			got, changed := multierr.Map(tc.err, func(err error) (error, bool) {
				require.Error(t, err)

				if err == errA { //nolint:errorlint // The test compares the error itself.
					return errC, true
				}

				return err, false
			})

			require.EqualError(t, got, tc.want)
			assert.Equal(t, tc.changed, changed)
			assert.Equal(t, multierr.IsBuilt(tc.err), multierr.IsBuilt(got))

			if !tc.changed {
				assert.Same(t, tc.err, got)
			}

			unwrapped := []error{errors.Unwrap(got)}
			if x, ok := got.(interface{ Unwrap() []error }); ok { //nolint:errorlint // The test reads the error itself.
				unwrapped = x.Unwrap()
			}

			assert.Equal(t, tc.unwraps, unwrapped)

			for _, err := range tc.is {
				require.ErrorIs(t, got, err)
			}

			for _, err := range tc.isNot {
				require.NotErrorIs(t, got, err)
			}

			if tc.as != nil {
				requireAs(t, got, tc.as)
			}
		})
	}
}
