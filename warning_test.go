package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// warningCafe is the document the warning tests check.
var warningCafe = stringtest.LinesLF(
	"seats: 40",
	"curency: SEK",
	"legacyPricing: true",
	"wifi: open",
)

// warnAtKey returns a validator that warns msg at the key name of the
// node it checks, and passes.
func warnAtKey(name, msg string) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning(msg, niceyaml.AtPath(paths.Current().Child(name).Key())))

		return nil
	})
}

// warningSpec is a type whose Validate method warns about a low seat count
// and fails on a negative one.
type warningSpec struct {
	Seats int `yaml:"seats"`
}

func (s warningSpec) Validate() error {
	switch {
	case s.Seats < 0:
		return yamltest.ErrorAt("seats must not be negative", "seats")
	case s.Seats < 10:
		return niceyaml.NewWarning("fewer than 10 seats", niceyaml.AtPath(paths.Current().Child("seats")))
	}

	return nil
}

// warningCafeDoc is the parent of a [warningSpec], whose Validate runs
// after the spec passes.
type warningCafeDoc struct {
	Spec  warningSpec `yaml:"spec"`
	Ran   *bool       `yaml:"-"`
	Extra string      `yaml:"extra"`
}

func (c warningCafeDoc) Validate() error {
	if c.Ran != nil {
		*c.Ran = true
	}

	return nil
}

// mixedSpec is a type whose Validate method returns an error beside a
// warning.
type mixedSpec struct {
	Seats int `yaml:"seats"`
}

func (mixedSpec) Validate() error {
	return errors.Join(
		yamltest.ErrorAt("seats must be even", "seats"),
		niceyaml.NewWarning("fewer than 10 seats", niceyaml.AtPath(paths.Current().Child("seats"))),
	)
}

// mixedCafeDoc is the parent of a [mixedSpec].
type mixedCafeDoc struct {
	Spec  mixedSpec `yaml:"spec"`
	Extra string    `yaml:"extra"`
}

// plainCafeDoc is the document of the warning tests, with no Validate
// method.
type plainCafeDoc struct {
	Extra string `yaml:"extra"`
}

// seatsOnly is a spec with no Validate method, which a function of
// WithValueCheck checks.
type seatsOnly struct {
	Seats int `yaml:"seats"`
}

// checkedCafeDoc is the parent of a [seatsOnly].
type checkedCafeDoc struct {
	Spec  seatsOnly `yaml:"spec"`
	Extra string    `yaml:"extra"`
}

// heldSpecDoc holds a spec that a decode does not reach. Its Validate
// checks the spec with SelfValidateValue and returns the warnings of that
// call beside its errors.
type heldSpecDoc struct {
	Spec  any    `yaml:"-"`
	Extra string `yaml:"extra"`
}

func (d heldSpecDoc) Validate() error {
	warnings := niceyaml.NewWarnings()

	err := niceyaml.SelfValidateValue(warnings.Context(context.Background()), d.Spec)

	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.Rebase(errors.Join(err, warnings.Err()), paths.Current().Child("spec"))
}

// hookedDoc runs its hook with the context of the decode that fills it.
type hookedDoc struct {
	hook func(ctx context.Context) error
}

func (d *hookedDoc) UnmarshalYAML(ctx context.Context, _ func(any) error) error {
	return d.hook(ctx)
}

// lintError is an aggregate that a validator returns over its failures.
type lintError struct{ errs []error }

func (e *lintError) Error() string { return errors.Join(e.errs...).Error() }

func (e *lintError) Unwrap() []error { return e.errs }

// warningRows returns a row for each warning that w holds, with its
// source, its position, its message, and the number of its details.
func warningRows(w *niceyaml.Warnings) []string {
	var rows []string

	for warning := range w.All() {
		at := "no position"
		if pos, ok := warning.Position(); ok {
			at = pos.String()
		}

		rows = append(rows, fmt.Sprintf("%s %s: %s [warning=%t, %d details]",
			warning.Source().Name(), at, warning.Message(), warning.Warning, len(warning.Children)))
	}

	return rows
}

func TestNewWarning(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))
	legacy := paths.Doc().Child("legacyPricing").Key()

	tcs := map[string]struct {
		err       error
		want      string
		message   string
		formatted string
	}{
		"bound at a path": {
			err:     doc.Bind(niceyaml.NewWarning(`deprecated field "legacyPricing"`, niceyaml.AtPath(legacy))),
			want:    `cafe.yaml:3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
			message: `deprecated field "legacyPricing"`,
			formatted: stringtest.JoinLF(
				`cafe.yaml:3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
				``,
				`   1 | seats: 40`,
				`   2 | curency: SEK`,
				`   3 | legacyPricing: true`,
				`     | ^^^^^^^^^^^^^`,
				`   4 | wifi: open`,
			),
		},
		"bound with no position": {
			err:     doc.Bind(niceyaml.NewWarning("old layout")),
			want:    "cafe.yaml: warning: old layout",
			message: "old layout",
		},
		"bound to no source": {
			err:     niceyaml.BindValue(niceyaml.NewWarning("old layout", niceyaml.AtPath(paths.Doc().Child("seats")))),
			want:    "warning: $.seats: old layout",
			message: "old layout",
		},
		"wrapped in a binding with context": {
			// The text of the wrapper joins the message, and the row that
			// the tree draws puts the place and the label in front of it.
			err:       fmt.Errorf("load: %w", doc.Bind(niceyaml.NewWarning("old layout"))),
			want:      "load: cafe.yaml: warning: old layout",
			message:   "load: old layout",
			formatted: "cafe.yaml: warning: load: old layout",
		},
		"unbound at an @ path": {
			err:       niceyaml.NewWarning("closes before it opens", niceyaml.AtPath(paths.Current().Child("close"))),
			want:      "closes before it opens",
			message:   "closes before it opens",
			formatted: "warning: @.close: closes before it opens",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())
			assert.True(t, niceyaml.IsInvalid(tc.err))
			assert.False(t, niceyaml.HasErrors(tc.err))

			problem := yamltest.OnlyProblem(t, tc.err)
			assert.True(t, problem.Warning)
			assert.Equal(t, tc.message, problem.Message())

			if tc.formatted != "" {
				assert.Equal(t, tc.formatted, niceyaml.FormatError(tc.err))
			}
		})
	}
}

func TestHasErrors(t *testing.T) {
	t.Parallel()

	warning := niceyaml.NewWarning("old")

	var (
		nilError       *niceyaml.Error
		nilSourceError *niceyaml.SourceError
	)

	tcs := map[string]struct {
		err  error
		want bool
	}{
		"nil":                      {err: nil, want: false},
		"nil Error pointer":        {err: nilError, want: false},
		"nil SourceError pointer":  {err: nilSourceError, want: false},
		"a warning":                {err: warning, want: false},
		"a join of warnings":       {err: errors.Join(warning, niceyaml.NewWarning("older")), want: false},
		"an error":                 {err: niceyaml.NewError("bad"), want: true},
		"a plain error":            {err: errors.New("bad"), want: true},
		"a join of both":           {err: errors.Join(warning, errors.New("bad")), want: true},
		"an error with no problem": {err: niceyaml.NewError(""), want: true},
		"a warning made an error":  {err: niceyaml.AsErrors(warning), want: true},
		"a wrapper with a sentinel and a warning": {
			err:  fmt.Errorf("%w; %w", fs.ErrNotExist, warning),
			want: false,
		},
		"a wrapper with an error and a warning": {
			err:  fmt.Errorf("%w; %w", niceyaml.NewError("bad"), warning),
			want: true,
		},
		"a wrapper with an unchecked error and a warning": {
			err:  fmt.Errorf("%w; %w", niceyaml.Unchecked(fs.ErrPermission), warning),
			want: true,
		},
		"a wrapper with an Invalid error and a warning": {
			err:  fmt.Errorf("%w; %w", niceyaml.Invalid(errors.New("bad")), warning),
			want: true,
		},
		"a warning among the details": {err: niceyaml.NewError("bad", niceyaml.WithDetails(warning)), want: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.HasErrors(tc.err))
		})
	}
}

func TestIsInvalid_Warnings(t *testing.T) {
	t.Parallel()

	warning := niceyaml.NewWarning("old")

	tcs := map[string]struct {
		err  error
		want bool
	}{
		"a warning alone answers for itself": {err: warning, want: true},
		"warnings beside an I/O error pass over the warnings": {
			err:  errors.Join(warning, fs.ErrPermission),
			want: false,
		},
		"warnings beside a fault pass over the warnings": {
			err:  errors.Join(niceyaml.Unchecked(niceyaml.NewWarning("x")), niceyaml.NewError("bad")),
			want: true,
		},
		"a warning that declares nothing answers for itself": {
			err:  niceyaml.Unchecked(niceyaml.AsErrors(fs.ErrPermission)),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err))
			assert.Equal(t, tc.want, niceyaml.NewErrorTree(tc.err).IsInvalid())
		})
	}
}

// loadAsErrorsError is an error with an AsErrors method of its own.
type loadAsErrorsError struct{ err error }

func (e loadAsErrorsError) Error() string { return e.err.Error() }

func (e loadAsErrorsError) Unwrap() error { return e.err }

func (e loadAsErrorsError) AsErrors() error { return loadAsErrorsError{err: niceyaml.AsErrors(e.err)} }

func TestAsErrors(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))
	legacy := niceyaml.NewWarning(`deprecated field "legacyPricing"`,
		niceyaml.AtPath(paths.Doc().Child("legacyPricing").Key()))
	unknown := niceyaml.NewError(`unknown field "wifi"`, niceyaml.AtPath(paths.Doc().Child("wifi").Key()))
	open := niceyaml.NewWarning("open network", niceyaml.AtPath(paths.Doc().Child("wifi")))

	require.NoError(t, niceyaml.AsErrors(nil))

	tcs := map[string]struct {
		err  error
		want string
		// Whether err is a wrapper that heads the warnings, so the result
		// matches err for errors.Is.
		wrapper bool
	}{
		"an error stays as it is": {
			err:  doc.Bind(unknown),
			want: `cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
		},
		"a bound warning loses its label": {
			err:  doc.Bind(legacy),
			want: `cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
		},
		"an unbound warning becomes an error": {
			err:  legacy,
			want: `deprecated field "legacyPricing"`,
		},
		"a join marks each warning": {
			err: doc.Bind(errors.Join(legacy, unknown)),
			want: stringtest.JoinLF(
				`cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
				`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
			),
		},
		"a type with a method of its own keeps its type": {
			err:  loadAsErrorsError{err: doc.Bind(legacy)},
			want: `cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
		},
		"a wrapped warning in a join loses its label": {
			// The row holds the text of the wrapper, which now marks an
			// error, so the label of the binding inside it stays out.
			err: doc.Bind(errors.Join(fmt.Errorf("lint: %w", doc.Bind(legacy)), unknown)),
			want: stringtest.JoinLF(
				`cafe.yaml:3:1: $.legacyPricing~: lint: deprecated field "legacyPricing"`,
				`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
			),
		},
		"a wrapper around a bound warning in a join of bindings reads as an error in the tree and the carets": {
			// The program wraps the binding, so the mark sits above the
			// wrapper, and the caret reads as the row does. Error() keeps
			// the text that fmt wrote, label included.
			err: errors.Join(fmt.Errorf("lint: %w", doc.Bind(legacy)), doc.Bind(unknown)),
			want: stringtest.JoinLF(
				`lint: cafe.yaml:3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
				`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
			),
		},
		"a wrapper around a join of warnings marks each warning": {
			err: fmt.Errorf("lint: %w", errors.Join(legacy, open)),
			want: stringtest.JoinLF(
				`lint: deprecated field "legacyPricing"`,
				"open network",
			),
			wrapper: true,
		},
		"a wrapper around a join of bound warnings marks each warning": {
			// The wrapper heads both bindings, so each binding gets the
			// mark and the text of the wrapper drops each label.
			err: fmt.Errorf("lint: %w", errors.Join(doc.Bind(legacy), doc.Bind(open))),
			want: stringtest.JoinLF(
				`lint: cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
				"cafe.yaml:4:7: $.wifi: open network",
			),
			wrapper: true,
		},
		"wrappers around a binding of a join of warnings mark each warning": {
			err: fmt.Errorf("check: %w", fmt.Errorf("lint: %w", doc.Bind(errors.Join(legacy, open)))),
			want: stringtest.JoinLF(
				`check: lint: cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
				"cafe.yaml:4:7: $.wifi: open network",
			),
			wrapper: true,
		},
		"a wrapper type of a caller's own around a join of bound warnings marks each warning": {
			err: &yamltest.CheckError{Err: errors.Join(doc.Bind(legacy), doc.Bind(open))},
			want: stringtest.JoinLF(
				`check: cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
				"cafe.yaml:4:7: $.wifi: open network",
			),
			wrapper: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.AsErrors(tc.err)
			assert.Equal(t, tc.want, got.Error())
			assert.True(t, niceyaml.HasErrors(got))

			if tc.wrapper {
				// The result matches the wrapper, and the type of a wrapper
				// of a caller's own.
				require.ErrorIs(t, got, tc.err)

				if errors.As(tc.err, new(*yamltest.CheckError)) {
					require.ErrorAs(t, got, new(*yamltest.CheckError))
				}
			}

			for problem := range niceyaml.Problems(got) {
				assert.False(t, problem.Warning)
				assert.NotContains(t, problem.Label, "warning:")
			}

			// The rows and the carets alike read as errors.
			assert.NotContains(t, niceyaml.FormatError(got), "warning:")

			for _, ex := range niceyaml.NewErrorReport(got).Excerpts {
				for i := range ex.View.All() {
					for _, ann := range ex.View.Annotations(i) {
						assert.Equal(t, kind.TextError, ann.Kind)
					}
				}
			}
		})
	}

	t.Run("a wrapper that rewrites the text it wraps keeps its message", func(t *testing.T) {
		t.Parallel()

		// The message of the wrapper does not hold the text of the join,
		// so the labels that the wrapper wrote stay in its message. Each
		// problem below it still reads as an error.
		wrapper := yamltest.IndentError{Err: errors.Join(doc.Bind(legacy), doc.Bind(open))}

		got := niceyaml.AsErrors(wrapper)
		assert.Equal(t, wrapper.Error(), got.Error())
		assert.True(t, niceyaml.HasErrors(got))
		require.ErrorIs(t, got, wrapper)

		var messages []string

		for problem := range niceyaml.Problems(got) {
			messages = append(messages, problem.Message())

			assert.False(t, problem.Warning)
			assert.NotContains(t, problem.Label, "warning:")
		}

		assert.Equal(t, []string{`deprecated field "legacyPricing"`, "open network"}, messages)

		for _, ex := range niceyaml.NewErrorReport(got).Excerpts {
			for i := range ex.View.All() {
				for _, ann := range ex.View.Annotations(i) {
					assert.Equal(t, kind.TextError, ann.Kind)
				}
			}
		}
	})
}

func TestAsErrors_WrapperBranches(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))
	legacy := niceyaml.NewWarning("legacy layout")
	port := niceyaml.NewWarning("legacy port")
	seats := niceyaml.NewWarning("few seats", niceyaml.AtPath(paths.Current().Child("seats")))
	errLegacy := errors.New("legacy")

	// AsErrors marks each problem that a wrapper with several %w verbs
	// heads, as it marks each branch of a join.
	tcs := map[string]struct {
		wrapped error
		joined  error
	}{
		"two warnings": {
			wrapped: fmt.Errorf("%w; %w", legacy, port),
			joined:  errors.Join(legacy, port),
		},
		"an error and a warning": {
			wrapped: fmt.Errorf("%w; %w", niceyaml.NewError("port is required"), legacy),
			joined:  errors.Join(niceyaml.NewError("port is required"), legacy),
		},
		"a located warning and a warning": {
			wrapped: fmt.Errorf("%w; %w", seats, legacy),
			joined:  errors.Join(seats, legacy),
		},
		"a summary and a warning": {
			wrapped: fmt.Errorf("%w; %w", niceyaml.NewSummary("2 problems", port, seats), legacy),
			joined:  errors.Join(niceyaml.NewSummary("2 problems", port, seats), legacy),
		},
		"a sentinel in front of a join": {
			wrapped: fmt.Errorf("%w: %w", errLegacy, errors.Join(legacy, port)),
			joined:  errors.Join(legacy, port),
		},
		"a rebase around two warnings": {
			wrapped: niceyaml.Rebase(fmt.Errorf("%w; %w", legacy, port), paths.Doc().Child("seats")),
			joined:  niceyaml.Rebase(errors.Join(legacy, port), paths.Doc().Child("seats")),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.AsErrors(tc.wrapped)
			assert.Equal(t, tc.wrapped.Error(), got.Error())
			require.ErrorIs(t, got, legacy)

			binds := map[string]func(error) error{
				"unbound": func(err error) error { return err },
				"bound":   doc.Bind,
			}

			for kind, bind := range binds {
				got := niceyaml.AsErrors(bind(tc.wrapped))
				want := niceyaml.AsErrors(bind(tc.joined))

				assert.True(t, niceyaml.HasErrors(got), kind)
				assert.ElementsMatch(t, severityRows(want), severityRows(got), kind)

				for problem := range niceyaml.Problems(got) {
					assert.False(t, problem.Warning, "%s: %s", kind, problem.Message())
				}
			}
		})
	}
}

func TestAsErrors_Tally(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))
	warnings := niceyaml.NewWarnings()

	_, err := doc.Decode[warningSpec](warnings.Context(t.Context()),
		niceyaml.WithValidator(warnAtKey("legacyPricing", `deprecated field "legacyPricing"`)),
		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
	require.Error(t, err)

	report := warnings.Report(err)
	assert.Equal(t, stringtest.JoinLF(
		"cafe.yaml: 3 errors, 1 warning",
		`cafe.yaml:2:1: $.curency~: unknown field "curency"`,
		`cafe.yaml:3:1: $.legacyPricing~: unknown field "legacyPricing"`,
		`cafe.yaml:3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
		`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
	), report.Error())

	assert.Equal(t, stringtest.JoinLF(
		"cafe.yaml: 4 errors",
		`cafe.yaml:2:1: $.curency~: unknown field "curency"`,
		`cafe.yaml:3:1: $.legacyPricing~: unknown field "legacyPricing"`,
		`cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
		`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
	), niceyaml.AsErrors(report).Error())
}

func TestWarnings(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: 4",
		"extra: x",
	), niceyaml.WithName("cafe.yaml"))
	failing := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: -1",
		"extra: x",
	), niceyaml.WithName("cafe.yaml"))
	rejected := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: 4",
		"extra: [x]",
	), niceyaml.WithName("cafe.yaml"))
	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"spec: 1",
		"---",
		"spec: 2",
		"extra: 3",
	), niceyaml.WithName("all.yaml"))

	warnExtra := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning("extra is unused", niceyaml.AtPath(paths.Current().Child("extra"))))

		return nil
	})
	oldSpec := warnAtKey("spec", "old spec")
	badExtra := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return yamltest.ErrorAt("bad extra", "extra")
	})
	returns := func(err error) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return err
		})
	}
	warnsAndFails := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key())))

		return yamltest.ErrorAt("bad extra", "extra")
	})
	secondFails := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key())))

		if n.DocumentIndex() == 1 {
			return yamltest.ErrorAt("bad extra", "extra")
		}

		return nil
	})

	// Each call hands every warning to the collector, whether it passes or
	// fails, and returns its errors alone.
	tcs := map[string]struct {
		run      func(t *testing.T, ctx context.Context) error
		err      string
		warnings string
	}{
		"a decode that passes": {
			run: func(t *testing.T, ctx context.Context) error {
				t.Helper()

				got, err := doc.Decode[warningCafeDoc](ctx, niceyaml.WithValidator(warnExtra))
				assert.Equal(t, 4, got.Spec.Seats)

				return err
			},
			warnings: stringtest.JoinLF(
				"cafe.yaml: 2 warnings",
				"cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
				"cafe.yaml:3:8: warning: $.extra: extra is unused",
			),
		},
		"a decode that fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				_, err := failing.Decode[warningCafeDoc](ctx, niceyaml.WithValidator(warnExtra))

				return err
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must not be negative",
			warnings: "cafe.yaml:3:8: warning: $.extra: extra is unused",
		},
		"a decode whose validator fails skips the Validate methods": {
			run: func(_ *testing.T, ctx context.Context) error {
				_, err := doc.Decode[warningCafeDoc](ctx, niceyaml.WithValidator(warnsAndFails))

				return err
			},
			err:      "cafe.yaml:3:8: $.extra: bad extra",
			warnings: "cafe.yaml:1:1: warning: $.spec~: old spec",
		},
		"a decode that the decoder rejects skips the Validate methods": {
			run: func(_ *testing.T, ctx context.Context) error {
				_, err := rejected.Decode[warningCafeDoc](ctx, niceyaml.WithValidator(warnExtra))

				return err
			},
			err:      "cafe.yaml:3:1: $.extra: expected string, got sequence",
			warnings: "cafe.yaml:3:1: warning: $.extra: extra is unused",
		},
		"a Validate result of an error and a warning": {
			run: func(_ *testing.T, ctx context.Context) error {
				_, err := doc.Decode[mixedCafeDoc](ctx)

				return err
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must be even",
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a validator result of warnings alone goes on to the next validator": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, niceyaml.ChainValidator(returns(niceyaml.NewWarning("old layout")), badExtra))
			},
			err:      "cafe.yaml:3:8: $.extra: bad extra",
			warnings: "cafe.yaml: warning: old layout",
		},
		"a validator result of an error and a warning": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, returns(errors.Join(
					yamltest.ErrorAt("bad extra", "extra"),
					niceyaml.NewWarning("old layout"),
				)))
			},
			err:      "cafe.yaml:3:8: $.extra: bad extra",
			warnings: "cafe.yaml: warning: old layout",
		},
		"a MultiValidator that fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, niceyaml.MultiValidator(oldSpec, badExtra))
			},
			err:      "cafe.yaml:3:8: $.extra: bad extra",
			warnings: "cafe.yaml:1:1: warning: $.spec~: old spec",
		},
		"one warning that two checks report": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, niceyaml.MultiValidator(oldSpec, oldSpec))
			},
			warnings: "cafe.yaml:1:1: warning: $.spec~: old spec",
		},
		"a SelfValidate that fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.SelfValidate(ctx, &mixedCafeDoc{})
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must be even",
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a SelfValidateValue that passes": {
			run: func(_ *testing.T, ctx context.Context) error {
				return niceyaml.SelfValidateValue(ctx, warningCafeDoc{Spec: warningSpec{Seats: 3}})
			},
			warnings: "warning: $.spec.seats: fewer than 10 seats",
		},
		"a SelfValidateValue that fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				return niceyaml.SelfValidateValue(ctx, mixedSpec{})
			},
			err:      "$.seats: seats must be even",
			warnings: "warning: $.seats: fewer than 10 seats",
		},
		"a SelfValidateValue that its context stops": {
			run: func(_ *testing.T, ctx context.Context) error {
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()

				return niceyaml.SelfValidateValue(ctx, warningSpec{Seats: 3},
					niceyaml.WithValueCheck(func(ctx context.Context, _ *warningSpec) error {
						cancel()

						return errors.Join(niceyaml.NewWarning("slow lookup"), ctx.Err())
					}))
			},
			err: "context canceled",
			warnings: stringtest.JoinLF(
				"2 warnings",
				"warning: $.seats: fewer than 10 seats",
				"warning: $: slow lookup",
			),
		},
		"a value check whose value call passes": {
			run: func(_ *testing.T, ctx context.Context) error {
				var got checkedCafeDoc

				return doc.DecodeInto(ctx, &got, niceyaml.WithValueCheck(func(ctx context.Context, s *seatsOnly) error {
					return niceyaml.SelfValidateValue(ctx, warningSpec{Seats: s.Seats})
				}))
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a value check whose value call fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				var got checkedCafeDoc

				return doc.DecodeInto(ctx, &got, niceyaml.WithValueCheck(func(ctx context.Context, s *seatsOnly) error {
					return niceyaml.SelfValidateValue(ctx, mixedSpec{Seats: s.Seats})
				}))
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must be even",
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a validator whose value call passes": {
			run: func(t *testing.T, ctx context.Context) error {
				t.Helper()

				spec := yamltest.At(t, doc, paths.Current().Child("spec"))

				return spec.Validate(ctx, niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
					return niceyaml.SelfValidateValue(ctx, warningSpec{Seats: 3})
				}))
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a Validate whose value call passes": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.DecodeInto(ctx, &heldSpecDoc{Spec: warningSpec{Seats: 3}})
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a Validate whose value call fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.DecodeInto(ctx, &heldSpecDoc{Spec: mixedSpec{}})
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must be even",
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a validator result of a wrapper around an error and a warning": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, returns(fmt.Errorf("checking: %w", errors.Join(
					yamltest.ErrorAt("bad extra", "extra"),
					niceyaml.NewWarning("old layout"),
				))))
			},
			err:      "cafe.yaml:3:8: $.extra: checking: bad extra",
			warnings: "cafe.yaml: warning: old layout",
		},
		"a warning of a wrapper around a join of errors": {
			// Warn marks each problem below the wrapper, so each error of
			// the join becomes a warning, and the call passes.
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
					n.Warn(ctx, fmt.Errorf("lint: %w", errors.Join(
						yamltest.ErrorAt("few seats", "spec", "seats"),
						yamltest.ErrorAt("bad extra", "extra"),
					)))

					return nil
				}))
			},
			warnings: stringtest.JoinLF(
				"cafe.yaml: 2 warnings",
				"cafe.yaml:2:10: warning: $.spec.seats: few seats",
				"cafe.yaml:3:8: warning: $.extra: bad extra",
			),
		},
		"a ValidateDocuments that fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				return source.ValidateDocuments(ctx, secondFails)
			},
			err: "all.yaml:4:8: $.extra: bad extra",
			warnings: stringtest.JoinLF(
				"all.yaml: 2 warnings",
				"all.yaml:1:1: warning: $.spec~: old spec",
				"all.yaml:3:1: warning: $.spec~: old spec",
			),
		},
		"a ValidateDocuments whose validator returns a warning without a location": {
			// The warning binds in each document, and each row names its
			// document.
			run: func(_ *testing.T, ctx context.Context) error {
				return source.ValidateDocuments(ctx, returns(niceyaml.NewWarning("old layout")))
			},
			warnings: stringtest.JoinLF(
				"all.yaml: 2 warnings",
				"all.yaml: document 1: warning: old layout",
				"all.yaml: document 2: warning: old layout",
			),
		},
		"a ValidateDocuments whose validator warns without a location": {
			run: func(_ *testing.T, ctx context.Context) error {
				warns := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
					n.Warn(ctx, niceyaml.NewWarning("old layout"))

					return nil
				})

				return source.ValidateDocuments(ctx, warns)
			},
			warnings: stringtest.JoinLF(
				"all.yaml: 2 warnings",
				"all.yaml: document 1: warning: old layout",
				"all.yaml: document 2: warning: old layout",
			),
		},
		"a ValidateDocuments whose validator warns at one position of the file": {
			// The position names the line in the file, so the warning that
			// each document binds there reads alike and keeps one row.
			run: func(_ *testing.T, ctx context.Context) error {
				warns := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
					n.Warn(ctx, niceyaml.NewWarning("long line", niceyaml.AtPosition(position.New(2, 0))))

					return nil
				})

				return source.ValidateDocuments(ctx, warns)
			},
			warnings: "all.yaml:3:1: warning: long line",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			check := func(t *testing.T, err error) {
				t.Helper()

				if tc.err == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.err)

				for problem := range niceyaml.Problems(err) {
					assert.False(t, problem.Warning, problem.Message())
				}
			}

			warnings := niceyaml.NewWarnings()

			check(t, tc.run(t, warnings.Context(t.Context())))

			got := warnings.Err()
			require.Error(t, got)
			assert.Equal(t, tc.warnings, got.Error())
			assert.False(t, niceyaml.HasErrors(got))

			// A call with no collector drops its warnings and returns the
			// same errors.
			check(t, tc.run(t, t.Context()))
		})
	}

	t.Run("a Validate whose values below it warned still runs", func(t *testing.T) {
		t.Parallel()

		ran := false
		parent := warningCafeDoc{Ran: &ran}

		require.NoError(t, doc.DecodeInto(t.Context(), &parent))
		assert.True(t, ran)
	})

	t.Run("a failure keeps its type beside a warning", func(t *testing.T) {
		t.Parallel()

		lint := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			n.Warn(ctx, niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key())))

			return &lintError{errs: []error{yamltest.ErrorAt("bad extra", "extra")}}
		})
		warnings := niceyaml.NewWarnings()

		err := doc.Validate(warnings.Context(t.Context()), niceyaml.MultiValidator(oldSpec, lint))
		require.EqualError(t, err, "cafe.yaml:3:8: $.extra: bad extra")

		require.ErrorAs(t, err, new(*lintError))
		assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", warnings.Err().Error())
	})

	t.Run("a failure that holds a warning keeps its type", func(t *testing.T) {
		t.Parallel()

		lint := returns(&lintError{errs: []error{
			yamltest.ErrorAt("bad extra", "extra"),
			niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key())),
		}})
		warnings := niceyaml.NewWarnings()

		err := doc.Validate(warnings.Context(t.Context()), lint)
		require.EqualError(t, err, "cafe.yaml:3:8: $.extra: bad extra")

		require.ErrorAs(t, err, new(*lintError))
		assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", warnings.Err().Error())
	})

	t.Run("calls in several goroutines share one collector", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()
		ctx := warnings.Context(t.Context())

		var wg sync.WaitGroup

		for _, key := range []string{"spec", "extra"} {
			wg.Go(func() {
				assert.NoError(t, doc.Validate(ctx, warnAtKey(key, "old "+key)))
			})
		}

		wg.Wait()

		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml: 2 warnings",
			"cafe.yaml:1:1: warning: $.spec~: old spec",
			"cafe.yaml:3:1: warning: $.extra~: old extra",
		), warnings.Err().Error())
	})

	t.Run("a detached copy gives way to the warning it copies", func(t *testing.T) {
		t.Parallel()

		old := func() error {
			return niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Doc().Child("spec").Key()))
		}
		unused := func() error {
			return niceyaml.NewWarning("extra is unused", niceyaml.AtPath(paths.Doc().Child("extra")))
		}

		// The collector keeps one row of the warning, whichever reaches it
		// first, and the row stays bound to the Source of doc.
		tcs := map[string]struct {
			warn []error
			want string
		}{
			"the warning first": {
				warn: []error{old(), niceyaml.Detach(doc.Bind(old()))},
				want: "cafe.yaml:1:1: warning: $.spec~: old spec",
			},
			"the copy first": {
				warn: []error{niceyaml.Detach(doc.Bind(old())), old()},
				want: "cafe.yaml:1:1: warning: $.spec~: old spec",
			},
			"two copies first": {
				warn: []error{niceyaml.Detach(doc.Bind(old())), niceyaml.Detach(doc.Bind(old())), old()},
				want: "cafe.yaml:1:1: warning: $.spec~: old spec",
			},
			"the copy first in one call": {
				warn: []error{errors.Join(niceyaml.Detach(doc.Bind(old())), old())},
				want: "cafe.yaml:1:1: warning: $.spec~: old spec",
			},
			"a copy of a join first": {
				warn: []error{
					niceyaml.Detach(doc.Bind(errors.Join(old(), unused()))),
					errors.Join(unused(), old()),
				},
				want: stringtest.JoinLF(
					"cafe.yaml: 2 warnings",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"cafe.yaml:3:8: warning: $.extra: extra is unused",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()
				ctx := warnings.Context(t.Context())

				for _, err := range tc.warn {
					doc.Warn(ctx, err)
				}

				got := warnings.Err()
				require.Error(t, got)
				assert.Equal(t, tc.want, got.Error())

				for warning := range warnings.All() {
					assert.Same(t, doc.Source(), warning.Source(), warning.Message())
				}

				assert.True(t, niceyaml.Annotate(got, doc.Source().View()))
			})
		}
	})

	t.Run("each document keeps a row of its warning", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"spec: 1",
			"---",
			"spec: 2",
		), niceyaml.WithName("all.yaml")).Documents()

		// The spec key holds a number, so this path does not resolve.
		deep := func() error {
			return niceyaml.NewWarning("old deep", niceyaml.AtPath(paths.Doc().Child("spec").Child("deep")))
		}
		layout := func() error {
			return niceyaml.NewWarning("old layout")
		}

		// A warning without a position keeps a row for each document it
		// binds in. A detached copy keeps its document, so it still gives
		// way to the warning it copies there.
		tcs := map[string]struct {
			warn func(ctx context.Context)
			want string
			// The DocumentIndex of each row that All yields, in order.
			docs []int
		}{
			"at a path that does not resolve": {
				warn: func(ctx context.Context) {
					docs[0].Warn(ctx, deep())
					docs[1].Warn(ctx, deep())
				},
				want: stringtest.JoinLF(
					"all.yaml: 2 warnings",
					"all.yaml: document 1: warning: $.spec.deep: old deep",
					"all.yaml: document 2: warning: $.spec.deep: old deep",
				),
				docs: []int{0, 1},
			},
			"without a location": {
				warn: func(ctx context.Context) {
					docs[0].Warn(ctx, layout())
					docs[1].Warn(ctx, layout())
				},
				want: stringtest.JoinLF(
					"all.yaml: 2 warnings",
					"all.yaml: document 1: warning: old layout",
					"all.yaml: document 2: warning: old layout",
				),
				docs: []int{0, 1},
			},
			"in one call": {
				warn: func(ctx context.Context) {
					docs[0].Warn(ctx, errors.Join(docs[0].Bind(layout()), docs[1].Bind(layout())))
				},
				want: stringtest.JoinLF(
					"all.yaml: 2 warnings",
					"all.yaml: document 1: warning: old layout",
					"all.yaml: document 2: warning: old layout",
				),
				docs: []int{0, 1},
			},
			"a detached copy beside the warning of another document": {
				warn: func(ctx context.Context) {
					docs[1].Warn(ctx, niceyaml.Detach(docs[1].Bind(deep())))
					docs[1].Warn(ctx, deep())
					docs[0].Warn(ctx, deep())
				},
				want: stringtest.JoinLF(
					"all.yaml: 2 warnings",
					"all.yaml: document 2: warning: $.spec.deep: old deep",
					"all.yaml: document 1: warning: $.spec.deep: old deep",
				),
				docs: []int{1, 0},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				tc.warn(warnings.Context(t.Context()))

				got := warnings.Err()
				require.Error(t, got)
				assert.Equal(t, tc.want, got.Error())

				var indexes []int

				for warning := range warnings.All() {
					index, ok := warning.DocumentIndex()
					assert.True(t, ok, warning.Message())
					assert.Same(t, docs[0].Source(), warning.Source(), warning.Message())

					indexes = append(indexes, index)
				}

				assert.Equal(t, tc.docs, indexes)
			})
		}
	})

	t.Run("a warning that reads alike keeps one row", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"spec: 1",
			"---",
			"spec: 2",
		), niceyaml.WithName("all.yaml")).Documents()
		one := yamltest.FirstDocument(t, "spec: 1\n", niceyaml.WithName("one.yaml"))

		// The third line of the file lies in the second document,
		// whichever document binds the warning.
		atLine := func() error {
			return niceyaml.NewWarning("long line", niceyaml.AtPosition(position.New(2, 0)))
		}
		plain := func() error {
			return niceyaml.NewWarning("plain")
		}
		// The file holds a single line, so this position does not resolve.
		far := func() error {
			return niceyaml.NewWarning("far", niceyaml.AtPosition(position.New(40, 0)))
		}

		// A position names the place of a warning in the file, so the
		// document that binds the warning does not set two rows apart. A
		// message without a position names its document only in a file of
		// several documents. In a file of one document, a warning that the
		// source binds outside the document thus reads alike with the same
		// warning that the document binds. The collector keeps the row that
		// reaches it first, unless a live row follows a detached copy.
		tcs := map[string]struct {
			warn func(ctx context.Context)
			want string
			// The DocumentIndex of the row that All yields.
			doc   int
			inDoc bool
		}{
			"at one position from each document": {
				warn: func(ctx context.Context) {
					docs[0].Warn(ctx, atLine())
					docs[1].Warn(ctx, atLine())
				},
				want:  "all.yaml:3:1: warning: long line",
				doc:   0,
				inDoc: true,
			},
			"at one position from each document in one call": {
				warn: func(ctx context.Context) {
					docs[1].Warn(ctx, errors.Join(docs[1].Bind(atLine()), docs[0].Bind(atLine())))
				},
				want:  "all.yaml:3:1: warning: long line",
				doc:   1,
				inDoc: true,
			},
			"without a location from the source and then the document": {
				warn: func(ctx context.Context) {
					one.Warn(ctx, one.Source().Bind(plain()))
					one.Warn(ctx, plain())
				},
				want:  "one.yaml: warning: plain",
				doc:   0,
				inDoc: false,
			},
			"without a location from the document and then the source": {
				warn: func(ctx context.Context) {
					one.Warn(ctx, plain())
					one.Warn(ctx, one.Source().Bind(plain()))
				},
				want:  "one.yaml: warning: plain",
				doc:   0,
				inDoc: true,
			},
			"a detached copy before the warning that the source binds": {
				warn: func(ctx context.Context) {
					one.Warn(ctx, niceyaml.Detach(one.Bind(plain())))
					one.Warn(ctx, one.Source().Bind(plain()))
				},
				want:  "one.yaml: warning: plain",
				doc:   0,
				inDoc: false,
			},
			"at a position out of range from the document and the source": {
				warn: func(ctx context.Context) {
					one.Warn(ctx, far())
					one.Warn(ctx, one.Source().Bind(far()))
				},
				want:  "one.yaml: warning: far",
				doc:   0,
				inDoc: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				tc.warn(warnings.Context(t.Context()))

				got := warnings.Err()
				require.Error(t, got)
				assert.Equal(t, tc.want, got.Error())

				rows := slices.Collect(warnings.All())
				require.Len(t, rows, 1)

				index, ok := rows[0].DocumentIndex()
				assert.Equal(t, tc.doc, index)
				assert.Equal(t, tc.inDoc, ok)
				assert.NotNil(t, rows[0].Source(), "the row of a live source")
			})
		}
	})
}

func TestWarnings_Context(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: 4",
		"extra: x",
	), niceyaml.WithName("cafe.yaml"))
	source := niceyaml.NewSourceFromString("extra: x\n", niceyaml.WithName("all.yaml"))
	oldSpec := warnAtKey("spec", "old spec")
	oldExtra := warnAtKey("extra", "old extra")

	validatorOf := func(hook func(context.Context) error) niceyaml.ValidatorFunc {
		return func(ctx context.Context, _ *niceyaml.Node) error {
			return hook(ctx)
		}
	}
	checkOf := func(hook func(context.Context) error) niceyaml.DecodeOption {
		return niceyaml.WithValueCheck(func(ctx context.Context, _ *plainCafeDoc) error {
			return hook(ctx)
		})
	}

	// Each call runs hook with the context it passes to a hook of its own.
	tcs := map[string]struct {
		call func(ctx context.Context, hook func(context.Context) error) error
	}{
		"Node.Validate runs a validator": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.Validate(ctx, validatorOf(hook))
			},
		},
		"ValidatorFunc.Check runs its function": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return validatorOf(hook).Check(ctx, doc)
			},
		},
		"Source.ValidateDocuments runs a validator": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return source.ValidateDocuments(ctx, validatorOf(hook))
			},
		},
		"Node.DecodeInto runs a validator": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.DecodeInto(ctx, &plainCafeDoc{}, niceyaml.WithValidator(validatorOf(hook)))
			},
		},
		"Node.DecodeInto runs an unmarshaler": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.DecodeInto(ctx, &hookedDoc{hook: hook})
			},
		},
		"Node.DecodeInto runs a value check": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.DecodeInto(ctx, &plainCafeDoc{}, checkOf(hook))
			},
		},
		"Node.SelfValidate runs a value check": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.SelfValidate(ctx, &plainCafeDoc{}, checkOf(hook))
			},
		},
		"SelfValidateValue runs a value check": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return niceyaml.SelfValidateValue(ctx, plainCafeDoc{}, checkOf(hook))
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outer := niceyaml.NewWarnings()
			inner := niceyaml.NewWarnings()
			innermost := niceyaml.NewWarnings()

			// The hook collects the warnings of each check it runs, and the
			// call still gets them. A collector derived from the one of the
			// hook hands its warnings to the call, but not to that one.
			err := tc.call(outer.Context(t.Context()), func(ctx context.Context) error {
				ctx = inner.Context(ctx)

				err := doc.Validate(ctx, oldSpec)
				if err == nil {
					err = doc.Validate(innermost.Context(ctx), oldExtra)
				}

				return err //nolint:wrapcheck // The test inspects the error as it is.
			})
			require.NoError(t, err)

			assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", inner.Err().Error())
			assert.Equal(t, "cafe.yaml:3:1: warning: $.extra~: old extra", innermost.Err().Error())
			assert.Equal(t, stringtest.JoinLF(
				"cafe.yaml: 2 warnings",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
				"cafe.yaml:3:1: warning: $.extra~: old extra",
			), outer.Err().Error())
		})
	}

	t.Run("a collector outside a call replaces the one before it", func(t *testing.T) {
		t.Parallel()

		outer := niceyaml.NewWarnings()
		inner := niceyaml.NewWarnings()

		ctx := inner.Context(outer.Context(t.Context()))

		require.NoError(t, doc.Validate(ctx, oldSpec))
		assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", inner.Err().Error())
		require.NoError(t, outer.Err())
	})

	t.Run("a nil collector leaves the context as it is", func(t *testing.T) {
		t.Parallel()

		var none *niceyaml.Warnings

		outer := niceyaml.NewWarnings()
		ctx := outer.Context(t.Context())

		assert.Equal(t, ctx, none.Context(ctx))
		require.NoError(t, doc.Validate(none.Context(ctx), oldSpec))
		assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", outer.Err().Error())
	})
}

func TestWarnings_All(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: 4",
		"extra: x",
	), niceyaml.WithName("cafe.yaml"))

	warnings := niceyaml.NewWarnings()

	// The validator warns out of source order.
	err := doc.Validate(warnings.Context(t.Context()), niceyaml.ValidatorFunc(
		func(ctx context.Context, n *niceyaml.Node) error {
			n.Warn(ctx, niceyaml.NewWarning("old layout"))
			n.Warn(ctx, niceyaml.NewWarning("extra is unused", niceyaml.AtPath(paths.Current().Child("extra"))))
			n.Warn(ctx, niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key()),
				niceyaml.WithDetails(errors.New("spec moves to v2"))))

			return nil
		},
	))
	require.NoError(t, err)

	assert.Equal(t, []string{
		"cafe.yaml 0:0: old spec [warning=true, 1 details]",
		"cafe.yaml 2:7: extra is unused [warning=true, 0 details]",
		"cafe.yaml no position: old layout [warning=true, 0 details]",
	}, warningRows(warnings))

	assert.Empty(t, warningRows(niceyaml.NewWarnings()))
	assert.Empty(t, warningRows(nil))
}

func TestWarnings_Err(t *testing.T) {
	t.Parallel()

	cafe := yamltest.FirstDocument(t, "seats: 4\nextra: x\n", niceyaml.WithName("cafe.yaml"))
	menu := yamltest.FirstDocument(t, "name: menu\n", niceyaml.WithName("menu.yaml"))

	warnAt := func(doc *niceyaml.Node, key string) func(t *testing.T, ctx context.Context) {
		return func(_ *testing.T, ctx context.Context) {
			doc.Warn(ctx, niceyaml.NewWarning("old "+key, niceyaml.AtPath(paths.Current().Child(key))))
		}
	}
	wrapAt := func(doc *niceyaml.Node, wrapper, key string) func(t *testing.T, ctx context.Context) {
		return func(_ *testing.T, ctx context.Context) {
			bound := doc.Bind(niceyaml.NewWarning("old "+key, niceyaml.AtPath(paths.Current().Child(key))))
			doc.Warn(ctx, fmt.Errorf("%s: %w", wrapper, bound))
		}
	}

	tcs := map[string]struct {
		warn []func(t *testing.T, ctx context.Context)
		want string
	}{
		"no warning": {},
		"one warning": {
			warn: []func(*testing.T, context.Context){warnAt(cafe, "extra")},
			want: "cafe.yaml:2:8: warning: $.extra: old extra",
		},
		"warnings of one source": {
			warn: []func(*testing.T, context.Context){warnAt(cafe, "extra"), warnAt(cafe, "seats")},
			want: stringtest.JoinLF(
				"cafe.yaml: 2 warnings",
				"cafe.yaml:1:8: warning: $.seats: old seats",
				"cafe.yaml:2:8: warning: $.extra: old extra",
			),
		},
		"warnings of no document": {
			warn: []func(*testing.T, context.Context){func(t *testing.T, ctx context.Context) {
				t.Helper()

				err := niceyaml.SelfValidateValue(ctx, warningCafeDoc{Spec: warningSpec{Seats: 3}})
				require.NoError(t, err)

				err = niceyaml.SelfValidateValue(ctx, warningSpec{Seats: 5})
				require.NoError(t, err)
			}},
			want: stringtest.JoinLF(
				"2 warnings",
				"warning: $.spec.seats: fewer than 10 seats",
				"warning: $.seats: fewer than 10 seats",
			),
		},
		"one warning twice": {
			warn: []func(*testing.T, context.Context){warnAt(cafe, "extra"), warnAt(cafe, "extra")},
			want: "cafe.yaml:2:8: warning: $.extra: old extra",
		},
		"one warning under two wrappers": {
			// The text of each wrapper joins the message of its row.
			warn: []func(*testing.T, context.Context){
				wrapAt(cafe, "lint", "extra"), wrapAt(cafe, "check", "extra"), wrapAt(cafe, "lint", "extra"),
			},
			want: stringtest.JoinLF(
				"cafe.yaml: 2 warnings",
				"cafe.yaml:2:8: warning: $.extra: lint: old extra",
				"cafe.yaml:2:8: warning: $.extra: check: old extra",
			),
		},
		"warnings of two sources": {
			warn: []func(*testing.T, context.Context){warnAt(menu, "name"), warnAt(cafe, "extra")},
			want: stringtest.JoinLF(
				"2 warnings",
				"menu.yaml:1:7: warning: $.name: old name",
				"cafe.yaml:2:8: warning: $.extra: old extra",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			warnings := niceyaml.NewWarnings()
			ctx := warnings.Context(t.Context())

			for _, warn := range tc.warn {
				warn(t, ctx)
			}

			got := warnings.Err()
			if tc.want == "" {
				require.NoError(t, got)

				return
			}

			require.Error(t, got)
			assert.Equal(t, tc.want, got.Error())
			assert.False(t, niceyaml.HasErrors(got))
		})
	}

	t.Run("a nil collector holds no warning", func(t *testing.T) {
		t.Parallel()

		var none *niceyaml.Warnings

		require.NoError(t, none.Err())
	})
}

func TestWarnings_Report(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: 4",
		"extra: x",
	), niceyaml.WithName("cafe.yaml"))
	oldSpec := niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Doc().Child("spec").Key()))
	badExtra := niceyaml.Invalid(fs.ErrPermission, niceyaml.AtPath(paths.Doc().Child("extra")))
	badSeats := yamltest.ErrorAt("bad seats", "spec", "seats")

	// The extra key holds a string, so this path does not resolve.
	oldDeep := niceyaml.NewWarning("old deep", niceyaml.AtPath(paths.Doc().Child("extra").Child("deep")))

	included := yamltest.FirstDocument(t, stringtest.LinesLF("y:", "  deep: 1"), niceyaml.WithName("inc.yaml"))
	oldY := included.Bind(niceyaml.NewWarning("old y", niceyaml.AtPath(paths.Doc().Child("y").Child("deep"))))

	tcs := map[string]struct {
		err  error
		warn []error
		// A sentinel that the report matches as err does.
		is   error
		want string
	}{
		"no warning leaves the error as it is": {
			err:  doc.Bind(badExtra),
			is:   fs.ErrPermission,
			want: "cafe.yaml:3:8: $.extra: permission denied",
		},
		"no error gives the warnings": {
			warn: []error{oldSpec},
			want: "cafe.yaml:1:1: warning: $.spec~: old spec",
		},
		"errors and warnings in source order": {
			err:  doc.Bind(errors.Join(badExtra, badSeats)),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 2 errors, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
				"cafe.yaml:2:10: $.spec.seats: bad seats",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a warning that the error holds shows once": {
			err:  doc.Bind(errors.Join(badExtra, oldSpec)),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a wrapped copy of a held warning shows once": {
			// The row of the error keeps the text of the wrapper.
			err:  errors.Join(fmt.Errorf("lint: %w", doc.Bind(oldSpec)), doc.Bind(badExtra)),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: lint: old spec",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a wrapped copy of a held warning in a bound join shows once": {
			// The source binds the wrapper again inside the join, and the
			// row of the error keeps the text of the wrapper.
			err:  doc.Bind(errors.Join(fmt.Errorf("lint: %w", doc.Bind(oldSpec)), badExtra)),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: lint: old spec",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a located copy of a held warning shows once": {
			// The location of the held warning did not resolve, so the
			// result of Locate gives it the location of the options. The
			// row of the error shows both.
			err: doc.Bind(errors.Join(
				niceyaml.Locate(doc.Bind(oldDeep), niceyaml.AtPath(paths.Doc().Child("spec"))),
				badExtra,
			)),
			warn: []error{oldDeep},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec: $.extra.deep: old deep",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a located copy of a held warning of an included file shows once": {
			// The result of Locate places the warning of the included file
			// at the value that includes it.
			err: doc.Bind(errors.Join(
				niceyaml.Locate(oldY, niceyaml.AtPath(paths.Doc().Child("spec"))),
				badExtra,
			)),
			warn: []error{oldY},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec: inc.yaml:2:9: $.y.deep: old y",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a located detached copy of a held warning shows once": {
			// The detached copy counts as a binding of cafe.yaml, so its row
			// reads as the row of the live copy does.
			err: doc.Bind(errors.Join(
				niceyaml.Locate(niceyaml.Detach(doc.Bind(oldDeep)), niceyaml.AtPath(paths.Doc().Child("spec"))),
				badExtra,
			)),
			warn: []error{oldDeep},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec: $.extra.deep: old deep",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a detached copy of a held warning shows once": {
			// The detached rows do not keep a Source, so BindValue binds
			// the heading.
			err:  niceyaml.Detach(doc.Bind(errors.Join(badExtra, oldSpec))),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"two wrappers of a held warning that the error does not hold": {
			err: doc.Bind(badExtra),
			warn: []error{
				fmt.Errorf("lint: %w", doc.Bind(oldSpec)),
				fmt.Errorf("check: %w", doc.Bind(oldSpec)),
			},
			is: fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 2 warnings",
				"cafe.yaml:1:1: warning: $.spec~: lint: old spec",
				"cafe.yaml:1:1: warning: $.spec~: check: old spec",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"a warning that wraps a bound error keeps its label": {
			// The wrapper adds its text to the row of the binding inside it,
			// and the row takes the severity of the wrapper.
			err:  doc.Bind(badExtra),
			warn: []error{fmt.Errorf("check: %w", doc.Bind(badSeats))},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:2:10: warning: $.spec.seats: check: bad seats",
				"cafe.yaml:3:8: $.extra: permission denied",
			),
		},
		"an error of no source binds the heading to no document": {
			err:  fmt.Errorf("read other.yaml: %w", fs.ErrNotExist),
			warn: []error{oldSpec},
			is:   fs.ErrNotExist,
			want: stringtest.JoinLF(
				"1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
				"read other.yaml: file does not exist",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			warnings := niceyaml.NewWarnings()
			ctx := warnings.Context(t.Context())

			for _, w := range tc.warn {
				doc.Warn(ctx, w)
			}

			got := warnings.Report(tc.err)
			require.Error(t, got)
			assert.Equal(t, tc.want, got.Error())

			if tc.err == nil {
				return
			}

			// The report keeps each problem of the error, and leaves the
			// error as it is.
			require.ErrorIs(t, got, tc.is)
			assert.Equal(t, niceyaml.HasErrors(tc.err), niceyaml.HasErrors(got))

			if len(tc.warn) == 0 {
				assert.Same(t, tc.err, got)
			}
		})
	}

	t.Run("a nil collector returns the error as it is", func(t *testing.T) {
		t.Parallel()

		var none *niceyaml.Warnings

		err := doc.Bind(badExtra)

		assert.Same(t, err, none.Report(err))
		require.NoError(t, none.Report(nil))
	})

	t.Run("a held warning shows unless err holds one that reads alike", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"spec: 1",
			"---",
			"spec: 2",
		), niceyaml.WithName("all.yaml"))
		docs := source.Documents()
		one := yamltest.FirstDocument(t, "spec: 1\n", niceyaml.WithName("one.yaml"))

		// The spec key holds a number, so this path does not resolve.
		deep := func() error {
			return niceyaml.NewWarning("old deep", niceyaml.AtPath(paths.Doc().Child("spec").Child("deep")))
		}
		layout := func() error {
			return niceyaml.NewWarning("old layout")
		}
		// The third line of the file lies in the second document.
		atLine := func() error {
			return niceyaml.NewWarning("long line", niceyaml.AtPosition(position.New(2, 0)))
		}
		bad := func() error {
			return docs[1].Bind(niceyaml.NewError("bad spec", niceyaml.AtPath(paths.Doc().Child("spec"))))
		}

		// A held warning shows unless err holds a copy of it at the same
		// position, or without a position in the document that the message
		// names. A message names the document only in a file of several.
		tcs := map[string]struct {
			held func(ctx context.Context)
			err  error
			want string
		}{
			"beside the same warning in another document": {
				held: func(ctx context.Context) { docs[0].Warn(ctx, deep()) },
				err:  errors.Join(docs[1].Bind(deep()), bad()),
				want: stringtest.JoinLF(
					"all.yaml: 1 error, 2 warnings",
					"all.yaml:3:7: $.spec: bad spec",
					"all.yaml: document 2: warning: $.spec.deep: old deep",
					"all.yaml: document 1: warning: $.spec.deep: old deep",
				),
			},
			"beside the same warning bound in no document": {
				held: func(ctx context.Context) { docs[0].Warn(ctx, layout()) },
				err:  errors.Join(source.Bind(layout()), bad()),
				want: stringtest.JoinLF(
					"all.yaml: 1 error, 2 warnings",
					"all.yaml:3:7: $.spec: bad spec",
					"all.yaml: warning: old layout",
					"all.yaml: document 1: warning: old layout",
				),
			},
			"beside a detached copy of it in another document": {
				held: func(ctx context.Context) { docs[0].Warn(ctx, deep()) },
				err:  niceyaml.Detach(errors.Join(docs[1].Bind(deep()), bad())),
				want: stringtest.JoinLF(
					"1 error, 2 warnings",
					"all.yaml:3:7: $.spec: bad spec",
					"all.yaml: document 2: warning: $.spec.deep: old deep",
					"all.yaml: document 1: warning: $.spec.deep: old deep",
				),
			},
			"not beside a detached copy of it in the same document": {
				held: func(ctx context.Context) { docs[1].Warn(ctx, deep()) },
				err:  niceyaml.Detach(errors.Join(docs[1].Bind(deep()), bad())),
				want: stringtest.JoinLF(
					"1 error, 1 warning",
					"all.yaml:3:7: $.spec: bad spec",
					"all.yaml: document 2: warning: $.spec.deep: old deep",
				),
			},
			"not beside the same warning at one position that another document binds": {
				held: func(ctx context.Context) { docs[0].Warn(ctx, atLine()) },
				err:  errors.Join(docs[1].Bind(atLine()), bad()),
				want: stringtest.JoinLF(
					"all.yaml: 1 error, 1 warning",
					"all.yaml:3:1: warning: long line",
					"all.yaml:3:7: $.spec: bad spec",
				),
			},
			"not beside a detached copy of it at one position that another document binds": {
				held: func(ctx context.Context) { docs[0].Warn(ctx, atLine()) },
				err:  niceyaml.Detach(errors.Join(docs[1].Bind(atLine()), bad())),
				want: stringtest.JoinLF(
					"1 error, 1 warning",
					"all.yaml:3:1: warning: long line",
					"all.yaml:3:7: $.spec: bad spec",
				),
			},
			"not beside the same warning that the source binds in a file of one document": {
				held: func(ctx context.Context) { one.Warn(ctx, layout()) },
				err: errors.Join(
					one.Source().Bind(layout()),
					one.Bind(niceyaml.NewError("bad spec", niceyaml.AtPath(paths.Doc().Child("spec")))),
				),
				want: stringtest.JoinLF(
					"one.yaml: 1 error, 1 warning",
					"one.yaml:1:7: $.spec: bad spec",
					"one.yaml: warning: old layout",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				tc.held(warnings.Context(t.Context()))

				got := warnings.Report(tc.err)
				require.Error(t, got)
				assert.Equal(t, tc.want, got.Error())
			})
		}
	})
}

func TestNode_Warn(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		items:
		  - price: 1
		  - price: 2
	`), niceyaml.WithName("menu.yaml"))
	item := yamltest.At(t, doc, paths.Current().Child("items").Index(1))

	tcs := map[string]struct {
		warn error
		want string
	}{
		"an @ path reads from the node": {
			warn: niceyaml.NewWarning("odd price", niceyaml.AtPath(paths.Current().Child("price"))),
			want: "menu.yaml:3:12: warning: $.items[1].price: odd price",
		},
		"an error becomes a warning": {
			warn: yamltest.ErrorAt("odd price", "price"),
			want: "menu.yaml:3:12: warning: $.items[1].price: odd price",
		},
		"a bound error becomes a warning": {
			warn: doc.Bind(niceyaml.NewError("odd", niceyaml.AtPath(paths.Doc().Child("items")))),
			want: "menu.yaml:1:1: warning: $.items: odd",
		},
		"a problem with no location binds at the node": {
			warn: errors.New("odd item"),
			want: "menu.yaml:3:3: warning: $.items[1]: odd item",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			warnings := niceyaml.NewWarnings()

			item.Warn(warnings.Context(t.Context()), tc.warn)

			got := warnings.Err()
			require.Error(t, got)
			assert.Equal(t, tc.want, got.Error())
			assert.False(t, niceyaml.HasErrors(got))
		})
	}

	t.Run("a wrapper around a bound error puts the label in its row", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		odd := doc.Bind(niceyaml.NewError("odd", niceyaml.AtPath(paths.Doc().Child("items"))))
		item.Warn(warnings.Context(t.Context()), fmt.Errorf("check: %w", odd))

		// The wrapper keeps the text that fmt wrote. The row of the tree
		// holds the text of the wrapper and takes its severity, although
		// the binding inside it is an error.
		got := warnings.Err()
		require.Error(t, got)
		assert.Equal(t, "check: menu.yaml:1:1: $.items: odd", got.Error())

		problem := yamltest.OnlyProblem(t, got)
		assert.True(t, problem.Warning)
		assert.Equal(t, "menu.yaml:1:1: warning: $.items: check: odd", problem.Label)
		assert.Equal(t, "check: odd", problem.Message())
	})

	t.Run("a wrapper with several verbs warns each problem it heads", func(t *testing.T) {
		t.Parallel()

		price := yamltest.ErrorAt("odd price", "price")
		odd := niceyaml.NewError("odd item")

		// The wrapper heads two problems, so each becomes a warning, as
		// each branch of a join does.
		var got []error

		for _, err := range []error{fmt.Errorf("%w; %w", price, odd), errors.Join(price, odd)} {
			warnings := niceyaml.NewWarnings()

			item.Warn(warnings.Context(t.Context()), err)

			warned := warnings.Err()
			require.Error(t, warned)
			assert.False(t, niceyaml.HasErrors(warned), warned.Error())

			got = append(got, warned)
		}

		assert.ElementsMatch(t, []string{
			"odd price [warning, invalid]",
			"odd item [warning, invalid]",
		}, severityRows(got[0]))
		assert.ElementsMatch(t, severityRows(got[1]), severityRows(got[0]))
	})

	t.Run("nil warns nothing", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		item.Warn(warnings.Context(t.Context()), nil)
		require.NoError(t, warnings.Err())
	})
}
