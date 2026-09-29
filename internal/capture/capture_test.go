package capture_test

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/capture"
)

var errSentinel = errors.New("sentinel")

func TestRun(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		fn  func() error
		err error
	}{
		"returns nil": {
			fn: func() error { return nil },
		},
		"returns an error": {
			fn:  func() error { return errSentinel },
			err: errSentinel,
		},
		"panics": {
			fn:  func() error { panic("bug") },
			err: &capture.PanicError{Value: "bug"},
		},
		"calls runtime.Goexit": {
			fn: func() error {
				runtime.Goexit()

				return nil
			},
			err: capture.ErrGoexit,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.err, capture.Run(tc.fn))
		})
	}
}

func TestReraise(t *testing.T) {
	t.Parallel()

	// Run captures whatever Reraise raises, so a Reraise that raises
	// nothing comes back as nil.
	tcs := map[string]struct {
		err  error
		want error
	}{
		"nil": {},
		"ordinary error": {
			err: errSentinel,
		},
		"panic": {
			err:  &capture.PanicError{Value: "bug"},
			want: &capture.PanicError{Value: "bug"},
		},
		"runtime.Goexit": {
			err:  capture.ErrGoexit,
			want: capture.ErrGoexit,
		},
		"wrapped panic": {
			err: fmt.Errorf("%w: %w", errSentinel, &capture.PanicError{Value: "bug"}),
		},
		"wrapped runtime.Goexit": {
			err: fmt.Errorf("%w: %w", errSentinel, capture.ErrGoexit),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := capture.Run(func() error {
				capture.Reraise(tc.err)

				return nil
			})
			assert.Equal(t, tc.want, got)
		})
	}
}
