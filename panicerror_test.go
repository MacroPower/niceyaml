package niceyaml_test

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// errRegistry is the error the code of a caller panics with.
var errRegistry = errors.New("registry not initialized")

// panicValueKey is the context key of the value a [panicsWith] panics
// with.
type panicValueKey struct{}

// panicCallsKey is the context key of the counter a [panicsWith] counts
// its calls in.
type panicCallsKey struct{}

// panicsWith decodes itself by panicking with the value the context of
// the decode holds under [panicValueKey]. It counts the call first, in
// the counter the context holds under [panicCallsKey].
type panicsWith struct{}

func (*panicsWith) UnmarshalYAML(ctx context.Context, _ []byte) error {
	if calls, ok := ctx.Value(panicCallsKey{}).(*atomic.Int32); ok {
		calls.Add(1)
	}

	panic(ctx.Value(panicValueKey{}))
}

// nilDereference decodes itself by reading through a nil pointer.
type nilDereference struct {
	next *nilDereference
}

func (d *nilDereference) UnmarshalYAML([]byte) error {
	*d = *d.next

	return nil
}

// panickingText is a string that panics when it decodes from text.
type panickingText string

func (*panickingText) UnmarshalText([]byte) error {
	panic("kind registry is empty")
}

// nestedDecode decodes itself with a decode of its own that panics, and
// returns the error of that decode.
type nestedDecode struct{}

func (*nestedDecode) UnmarshalYAML(ctx context.Context, text []byte) error {
	inner := niceyaml.NewSourceFromBytes(text, niceyaml.WithName("inner.yaml"))

	_, err := inner.Decode[panickingUnmarshaler](ctx)

	return err
}

// panickingValidator panics with errRegistry when it validates itself.
type panickingValidator struct {
	Name string `yaml:"name"`
}

func (panickingValidator) Validate() error {
	panic(errRegistry)
}

// requirePanic checks that err holds a PanicError with a stack, and
// that the document is not at fault for err. It returns the value of the
// panic and its stack.
func requirePanic(t *testing.T, err error) (any, string) {
	t.Helper()

	var p *niceyaml.PanicError

	require.ErrorAs(t, err, &p)
	require.NotEmpty(t, p.Stack)
	require.False(t, niceyaml.IsInvalid(err), "the document is at fault for a panic")

	return p.Value, string(p.Stack)
}

func TestPanicError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value  any
		unwrap error
		want   string
	}{
		"string": {
			value: "boom",
			want:  "panic: boom",
		},
		"number": {
			value: 42,
			want:  "panic: 42",
		},
		"error": {
			value:  errRegistry,
			unwrap: errRegistry,
			want:   "panic: registry not initialized",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := &niceyaml.PanicError{Value: tc.value, Stack: []byte("goroutine 1 [running]:")}

			require.EqualError(t, p, tc.want)
			assert.Equal(t, tc.unwrap, errors.Unwrap(p))
		})
	}
}

func TestPanicError_Recovered(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	tcs := map[string]struct {
		decode func(t *testing.T) error
		// An error the code panicked with, which the result matches.
		is error
		// Text the stack holds, which names the code that panicked.
		stack string
		want  string
	}{
		"function from WithCustomUnmarshaler": {
			decode: func(t *testing.T) error {
				t.Helper()

				fn := niceyaml.WithCustomUnmarshaler(func(context.Context, *config, func(any) error) error {
					panic(errRegistry)
				})

				_, err := yamltest.FirstDocument(t, "name: x\n", niceyaml.WithName("c.yaml")).
					Decode[config](t.Context(), fn)

				return err
			},
			is:   errRegistry,
			want: "c.yaml:1:1: decoder panicked: registry not initialized",
		},
		"nil dereference in UnmarshalYAML": {
			decode: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "v: x\n").Decode[struct {
					V nilDereference `yaml:"v"`
				}](t.Context())

				return err
			},
			stack: "nilDereference).UnmarshalYAML",
			want:  "1:1: decoder panicked: runtime error: invalid memory address or nil pointer dereference",
		},
		"UnmarshalText": {
			decode: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "kind: Deployment\n").Decode[struct {
					Kind panickingText `yaml:"kind"`
				}](t.Context())

				return err
			},
			stack: "panickingText).UnmarshalText",
			want:  "1:1: decoder panicked: kind registry is empty",
		},
		// The go-yaml decoder indexes the array past its end, so the
		// document alone trips this panic.
		"sequence longer than its array": {
			decode: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "# pair\n[a, b, c]\n").Decode[[2]string](t.Context())

				return err
			},
			stack: "go-yaml",
			want:  "2:1: decoder panicked: reflect: array index out of range",
		},
		// The decode of the outer value would mark the error of an
		// unmarshaler as a rejection, which the document is at fault for.
		"error an unmarshaler returns from a decode of its own": {
			decode: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "v: x\n").Decode[struct {
					V nestedDecode `yaml:"v"`
				}](t.Context())

				return err
			},
			is:   errUnmarshal,
			want: "inner.yaml:1:1: decoder panicked: unmarshaler rejected the value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var err error

			require.NotPanics(t, func() { err = tc.decode(t) })
			require.EqualError(t, err, tc.want)
			require.NotErrorIs(t, err, niceyaml.ErrDecode)
			require.NotErrorIs(t, err, niceyaml.ErrSyntax)

			value, stack := requirePanic(t, err)
			assert.Contains(t, stack, tc.stack)

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
				assert.Equal(t, tc.is, value)
			}

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr)
		})
	}

	t.Run("value of a runtime panic is a runtime error", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "v: x\n").Decode[struct {
			V nilDereference `yaml:"v"`
		}](t.Context())

		value, _ := requirePanic(t, err)

		_, ok := value.(runtime.Error)
		assert.True(t, ok, "got %T", value)

		var runtimeErr runtime.Error

		require.ErrorAs(t, err, &runtimeErr)
	})

	// The decode placed the panic already, so it calls no unmarshaler a
	// second time to find the value behind the error.
	t.Run("decode calls a value that panics once", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		ctx := context.WithValue(t.Context(), panicCallsKey{}, &calls)
		ctx = context.WithValue(ctx, panicValueKey{}, errRegistry)

		_, err := yamltest.FirstDocument(t, "items:\n  - v: 1\n").Decode[struct {
			Items []struct {
				V panicsWith `yaml:"v"`
			} `yaml:"items"`
		}](ctx)
		require.EqualError(t, err, "1:1: decoder panicked: registry not initialized")
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("panic beside another problem", func(t *testing.T) {
		t.Parallel()

		// The decoder rejects the port and goes on to the value that
		// panics. The search then finds the port again.
		_, err := yamltest.FirstDocument(t, "port: abc\nv: x\n").Decode[struct {
			Port int                  `yaml:"port"`
			V    panickingUnmarshaler `yaml:"v"`
		}](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"1:1: decoder panicked: unmarshaler rejected the value",
			"1:7: $.port: expected integer, got string",
		))

		// The rejection of the port matches, and the panic keeps the error
		// from being invalid.
		require.ErrorIs(t, err, niceyaml.ErrDecode)
		requirePanic(t, err)

		invalid := map[string]bool{}

		for problem := range niceyaml.NewErrorTree(err).Problems() {
			invalid[problem.Message()] = problem.IsInvalid()

			var p *niceyaml.PanicError

			assert.Equal(t, !problem.IsInvalid(), errors.As(problem.Err, &p), problem.Text)
		}

		assert.Equal(t, map[string]bool{
			"decoder panicked: unmarshaler rejected the value": false,
			"expected integer, got string":                     true,
		}, invalid)
	})
}

func TestPanicError_Recovered_Value(t *testing.T) {
	t.Parallel()

	// Each value is an error the document is at fault for on its own, or
	// one that heads such errors.
	decodeErr := func(t *testing.T) error {
		t.Helper()

		_, err := niceyaml.NewSourceFromString("port: abc\n", niceyaml.WithName("other.yaml")).
			Decode[struct {
			Port int `yaml:"port"`
		}](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrDecode)
		require.True(t, niceyaml.IsInvalid(err))

		return err
	}

	syntaxErr := func(t *testing.T) error {
		t.Helper()

		_, err := niceyaml.NewSourceFromString("a: [1\n", niceyaml.WithName("other.yaml")).File()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.True(t, niceyaml.IsInvalid(err))

		return err
	}

	tcs := map[string]struct {
		value func(t *testing.T) error
	}{
		"Error from NewError at a path": {
			value: func(*testing.T) error {
				return niceyaml.NewError("name taken", niceyaml.AtPath(paths.Doc().Child("name")))
			},
		},
		"summary of two Errors": {
			value: func(*testing.T) error {
				return niceyaml.NewSummary("2 problems", niceyaml.NewError("a"), niceyaml.NewError("b"))
			},
		},
		"join of two Errors": {
			value: func(*testing.T) error {
				return errors.Join(niceyaml.NewError("a"), niceyaml.NewError("b"))
			},
		},
		"decode error of another source": {
			value: decodeErr,
		},
		"syntax error of another source": {
			value: syntaxErr,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			value := tc.value(t)
			ctx := context.WithValue(t.Context(), panicValueKey{}, value)

			_, err := yamltest.FirstDocument(t, "name: x\nv: 1\n").Decode[struct {
				Name string     `yaml:"name"`
				V    panicsWith `yaml:"v"`
			}](ctx)

			got, _ := requirePanic(t, err)
			assert.Equal(t, value, got)

			// The value is in reach of errors.Is, and it decides neither
			// the stage nor the fault.
			require.ErrorIs(t, err, value)
			require.NotErrorIs(t, err, niceyaml.ErrDecode)
			require.NotErrorIs(t, err, niceyaml.ErrSyntax)

			// The panic is one problem at the node, whatever the value
			// points at or heads.
			var problems []niceyaml.ErrorTree

			for problem := range niceyaml.NewErrorTree(err).Problems() {
				problems = append(problems, problem)
			}

			require.Len(t, problems, 1)
			assert.False(t, problems[0].IsInvalid())

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr)

			pos, ok := srcErr.Position()
			require.True(t, ok, "the panic has no position")
			assert.Equal(t, 0, pos.Line)
			assert.Equal(t, 0, pos.Col)
		})
	}
}

func TestPanicError_Validators(t *testing.T) {
	t.Parallel()

	// Nothing recovers a panic outside the decoder, so it reaches the
	// caller as the panic it is.
	t.Run("panic in a Validator reaches the caller", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "name: x\n")

		panics := niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			panic(errRegistry)
		}))

		require.PanicsWithValue(t, errRegistry, func() {
			_, _ = doc.Decode[any](t.Context(), panics) //nolint:errcheck // The call panics before it returns.
		})
	})

	t.Run("panic in a Validate method reaches the caller", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "name: x\n")

		require.PanicsWithValue(t, errRegistry, func() {
			_, _ = doc.Decode[panickingValidator](t.Context()) //nolint:errcheck // The call panics before it returns.
		})
	})
}
