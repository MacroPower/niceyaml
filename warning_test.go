package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"slices"
	"strings"
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
// after the spec.
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

// wrappedSpec is a type whose Validate method returns its problems below
// one wrapper. Seats that are not set give an error, and so do tables
// that are not set. A patio that is not set gives a warning.
type wrappedSpec struct {
	Seats  int `yaml:"seats"`
	Tables int `yaml:"tables"`
	Patio  int `yaml:"patio"`
}

func (s wrappedSpec) Validate() error {
	var errs []error

	if s.Seats == 0 {
		errs = append(errs, yamltest.ErrorAt("seats not set", "seats"))
	}

	if s.Tables == 0 {
		errs = append(errs, yamltest.ErrorAt("tables not set", "tables"))
	}

	if s.Patio == 0 {
		errs = append(errs, niceyaml.NewWarning("patio not set", niceyaml.AtPath(paths.Current().Child("patio"))))
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("spec: %w", errors.Join(errs...))
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

// heldSpecContextDoc holds a spec that a decode does not reach. Its
// ValidateContext checks the spec with SelfValidateValue and its own
// context, through a struct that holds the spec under its name.
type heldSpecContextDoc struct {
	Spec  any    `yaml:"-"`
	Extra string `yaml:"extra"`
}

func (d heldSpecContextDoc) ValidateContext(ctx context.Context) error {
	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.SelfValidateValue(ctx, struct {
		Spec any `yaml:"spec"`
	}{d.Spec})
}

// heldSpecHookDoc holds a spec that a decode does not reach. Its
// ValidateContext checks the spec with SelfValidateValue and a collector
// derived from its context. It moves the result under the path of the spec
// and returns the warnings beside the errors.
type heldSpecHookDoc struct {
	Spec  any    `yaml:"-"`
	Extra string `yaml:"extra"`
}

func (d heldSpecHookDoc) ValidateContext(ctx context.Context) error {
	warnings := niceyaml.NewWarnings()

	err := niceyaml.SelfValidateValue(warnings.Context(ctx), d.Spec)

	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.Rebase(errors.Join(err, warnings.Err()), paths.Current().Child("spec"))
}

// notedError wraps an error in a value that a map cannot hash, because it
// holds a slice.
type notedError struct {
	err   error
	notes []string
}

func (e notedError) Error() string { return strings.Join(e.notes, ", ") + ": " + e.err.Error() }

func (e notedError) Unwrap() error { return e.err }

// contextSpec is a [warningSpec] whose check takes the context of the
// call and returns its warnings beside its errors.
type contextSpec struct {
	Seats int `yaml:"seats"`
}

func (s contextSpec) ValidateContext(context.Context) error {
	return warningSpec(s).Validate()
}

// contextCafeDoc is the parent of a [contextSpec].
type contextCafeDoc struct {
	Spec  contextSpec `yaml:"spec"`
	Extra string      `yaml:"extra"`
}

// hookedDoc runs its hook with the context of the decode that fills it.
type hookedDoc struct {
	hook func(ctx context.Context) error
}

func (d *hookedDoc) UnmarshalYAML(ctx context.Context, _ func(any) error) error {
	return d.hook(ctx)
}

// selfHookedDoc runs its hook with the context that its ValidateContext
// gets.
type selfHookedDoc struct {
	hook  func(ctx context.Context) error
	Extra string `yaml:"extra"`
}

func (d selfHookedDoc) ValidateContext(ctx context.Context) error {
	return d.hook(ctx)
}

// lintError is an aggregate that a validator returns over its failures.
type lintError struct{ errs []error }

func (e *lintError) Error() string { return errors.Join(e.errs...).Error() }

func (e *lintError) Unwrap() []error { return e.errs }

// ruleSetError is an aggregate under a heading that names its rule set.
// Its WithoutWarnings method keeps its type and its heading.
type ruleSetError struct {
	set  string
	errs []error
}

func (e *ruleSetError) Error() string {
	return "ruleset " + e.set
}

func (e *ruleSetError) Unwrap() []error { return e.errs }

func (e *ruleSetError) WithoutWarnings() error {
	var fails []error

	for _, f := range e.errs {
		f = niceyaml.WithoutWarnings(f)
		if f != nil {
			fails = append(fails, f)
		}
	}

	return &ruleSetError{set: e.set, errs: fails}
}

// strayRuleSetError is a [ruleSetError] whose WithoutWarnings method
// returns what split returns, such as nil or the receiver.
type strayRuleSetError struct {
	split func(e *strayRuleSetError) error
	errs  []error
}

func (e *strayRuleSetError) Error() string {
	return "ruleset stray"
}

func (e *strayRuleSetError) Unwrap() []error { return e.errs }

func (e *strayRuleSetError) WithoutWarnings() error { return e.split(e) }

// plainRuleSetError is a [ruleSetError] without the WithoutWarnings
// method.
type plainRuleSetError struct {
	set  string
	errs []error
}

func (e *plainRuleSetError) Error() string {
	return "ruleset " + e.set + ":\n" + errors.Join(e.errs...).Error()
}

func (e *plainRuleSetError) Unwrap() []error { return e.errs }

// blankRuleSetError is a [plainRuleSetError] with empty text.
type blankRuleSetError struct{ errs []error }

func (e *blankRuleSetError) Error() string { return "" }

func (e *blankRuleSetError) Unwrap() []error { return e.errs }

// ruleSetSpec is a type whose Validate method returns a [ruleSetError]
// over an error and a warning.
type ruleSetSpec struct {
	Seats int `yaml:"seats"`
}

func (ruleSetSpec) Validate() error {
	return &ruleSetError{set: "net", errs: []error{
		niceyaml.NewWarning("fewer than 10 seats", niceyaml.AtPath(paths.Current().Child("seats"))),
		yamltest.ErrorAt("seats must be even", "seats"),
	}}
}

// ruleSetCafeDoc is the parent of a [ruleSetSpec].
type ruleSetCafeDoc struct {
	Spec  ruleSetSpec `yaml:"spec"`
	Extra string      `yaml:"extra"`
}

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

// contextRows returns a row for each problem of err, with the context and
// the message that a report carries for it.
func contextRows(err error) []string {
	var rows []string

	for problem := range niceyaml.Problems(err) {
		rows = append(rows, fmt.Sprintf("[%s] %s", problem.Context(), problem.Message()))
	}

	return rows
}

// heldContextRows returns a row for each warning that w holds, with the
// context and the message that a report carries for it.
func heldContextRows(w *niceyaml.Warnings) []string {
	var rows []string

	for warning := range w.All() {
		rows = append(rows, fmt.Sprintf("[%s] %s", warning.Context(), warning.Message()))
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
			// The row that the tree draws puts the place and the label in
			// front of the text of the wrapper. The message of the problem
			// leaves that text out.
			err:       fmt.Errorf("load: %w", doc.Bind(niceyaml.NewWarning("old layout"))),
			want:      "load: cafe.yaml: warning: old layout",
			message:   "old layout",
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
				assert.NotContains(t, problem.Label(), "warning:")
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
			assert.NotContains(t, problem.Label(), "warning:")
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

func TestAsWarnings(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))
	unknown := niceyaml.NewError(`unknown field "wifi"`, niceyaml.AtPath(paths.Doc().Child("wifi").Key()))
	seats := niceyaml.NewError("too many seats", niceyaml.AtPath(paths.Doc().Child("seats")))

	require.NoError(t, niceyaml.AsWarnings(nil))

	tcs := map[string]struct {
		err  error
		want string
	}{
		"a bound error becomes a warning": {
			err:  doc.Bind(unknown),
			want: `cafe.yaml:4:1: warning: $.wifi~: unknown field "wifi"`,
		},
		"an unbound error becomes a warning that binds as one": {
			err:  unknown,
			want: `cafe.yaml:4:1: warning: $.wifi~: unknown field "wifi"`,
		},
		"a join marks each error": {
			err: doc.Bind(errors.Join(seats, unknown)),
			want: stringtest.JoinLF(
				`cafe.yaml:1:8: warning: $.seats: too many seats`,
				`cafe.yaml:4:1: warning: $.wifi~: unknown field "wifi"`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.AsWarnings(tc.err)
			require.Error(t, got)
			assert.False(t, niceyaml.HasErrors(got))
			assert.Equal(t, tc.want, doc.Bind(got).Error())
		})
	}

	t.Run("a typed error keeps its type", func(t *testing.T) {
		t.Parallel()

		got := niceyaml.AsWarnings(niceyaml.Invalid(&ruleError{id: "R1"},
			niceyaml.AtPath(paths.Current().Child("seats"))))
		assert.False(t, niceyaml.HasErrors(got))

		rule, ok := errors.AsType[*ruleError](got)
		require.True(t, ok)
		assert.Equal(t, "R1", rule.id)
		assert.Equal(t, "cafe.yaml:1:8: warning: $.seats: rule R1", doc.Bind(got).Error())
	})

	t.Run("a heading counts the warnings again", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		_, err := doc.Decode[warningSpec](warnings.Context(t.Context()),
			niceyaml.WithValidator(warnAtKey("legacyPricing", `deprecated field "legacyPricing"`)),
			niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
		require.Error(t, err)

		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml: 4 warnings",
			`cafe.yaml:2:1: warning: $.curency~: unknown field "curency"`,
			`cafe.yaml:3:1: warning: $.legacyPricing~: unknown field "legacyPricing"`,
			`cafe.yaml:3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
			`cafe.yaml:4:1: warning: $.wifi~: unknown field "wifi"`,
		), niceyaml.AsWarnings(warnings.Report(err)).Error())
	})

	t.Run("a check that returns a typed warning passes", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		_, err := doc.Decode[warningSpec](warnings.Context(t.Context()),
			niceyaml.WithValueCheck(func(context.Context, *warningSpec) error {
				return niceyaml.AsWarnings(niceyaml.Invalid(&ruleError{id: "R1"},
					niceyaml.AtPath(paths.Current().Child("seats"))))
			}))
		require.NoError(t, err)

		got := warnings.Err()
		assert.Equal(t, "cafe.yaml:1:8: warning: $.seats: rule R1", got.Error())

		var rule *ruleError

		assert.ErrorAs(t, got, &rule)
	})
}

func TestWarnValue(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))
	seats := paths.Current().Child("seats")

	tcs := map[string]struct {
		warn error
		want string
	}{
		"an error becomes a warning bound to the value": {
			warn: niceyaml.NewError("too many seats", niceyaml.AtPath(seats)),
			want: "warning: $.seats: too many seats",
		},
		"a warning stays a warning": {
			warn: niceyaml.NewWarning("too many seats", niceyaml.AtPath(seats)),
			want: "warning: $.seats: too many seats",
		},
		"a bound error keeps its binding": {
			warn: doc.Bind(niceyaml.NewError("too many seats", niceyaml.AtPath(seats))),
			want: "cafe.yaml:1:8: warning: $.seats: too many seats",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			warnings := niceyaml.NewWarnings()

			niceyaml.WarnValue(warnings.Context(t.Context()), tc.warn)

			got := warnings.Err()
			require.Error(t, got)
			assert.Equal(t, tc.want, got.Error())
			assert.False(t, niceyaml.HasErrors(got))
		})
	}

	t.Run("a nil error reports nothing", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		niceyaml.WarnValue(warnings.Context(t.Context()), nil)
		require.NoError(t, warnings.Err())
	})

	t.Run("a context without a collector drops the warning", func(t *testing.T) {
		t.Parallel()

		assert.NotPanics(t, func() {
			niceyaml.WarnValue(t.Context(), niceyaml.NewError("too many seats"))
		})
	})

	t.Run("a decode places the warning of a value check", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		_, err := doc.Decode[warningSpec](warnings.Context(t.Context()),
			niceyaml.WithValueCheck(func(ctx context.Context, _ *warningSpec) error {
				niceyaml.WarnValue(ctx, niceyaml.NewError("too many seats", niceyaml.AtPath(seats)))

				return nil
			}))
		require.NoError(t, err)

		got := warnings.Err()
		require.Error(t, got)
		assert.Equal(t, "cafe.yaml:1:8: warning: $.seats: too many seats", got.Error())
	})
}

func TestWithoutWarnings(t *testing.T) {
	t.Parallel()

	fail := errors.New("bad seats")
	other := errors.New("bad table")
	warn := niceyaml.NewWarning("few seats")
	old := niceyaml.NewWarning("old layout")

	tcs := map[string]struct {
		err error
		// The message of the result, or "" for a nil result.
		want string
		// Whether the result is a [*ruleSetError].
		ruleSet bool
	}{
		"nil": {},
		"error alone": {
			err:  fail,
			want: "bad seats",
		},
		"warnings alone": {
			err: errors.Join(warn, old),
		},
		"join of an error and a warning": {
			err:  errors.Join(warn, fail),
			want: "bad seats",
		},
		"wrapper around an error and a warning": {
			err:  fmt.Errorf("check: %w", errors.Join(fail, warn)),
			want: "check: bad seats",
		},
		"wrapper that shows the wrapped text twice": {
			// The wrapper does not split, because only one of the two
			// copies of the text comes from the wrapped error.
			err:  fmt.Errorf("%s | %w", errors.Join(fail, warn).Error(), errors.Join(fail, warn)),
			want: "bad seats",
		},
		"wrapper around a type with empty text": {
			// The wrapper does not split, because its text does not show
			// where the text of the type sits.
			err:  fmt.Errorf("%w", &blankRuleSetError{errs: []error{fail, warn}}),
			want: "bad seats",
		},
		"type with a WithoutWarnings method": {
			// The method splits each finding, and the type keeps its
			// heading over the error that a finding holds beside a warning.
			err:     &ruleSetError{set: "net", errs: []error{old, errors.Join(warn, fail)}},
			want:    "ruleset net",
			ruleSet: true,
		},
		"wrapper around a type with a WithoutWarnings method": {
			// The method keeps the text of the type, so the wrapper keeps
			// its text too.
			err:     fmt.Errorf("check: %w", &ruleSetError{set: "net", errs: []error{warn, fail}}),
			want:    "check: ruleset net",
			ruleSet: true,
		},
		"join branch with a WithoutWarnings method": {
			err:     errors.Join(&ruleSetError{set: "net", errs: []error{warn, fail}}, other),
			want:    "ruleset net\nbad table",
			ruleSet: true,
		},
		"type without a WithoutWarnings method": {
			err:  &plainRuleSetError{set: "net", errs: []error{old, fail}},
			want: "bad seats",
		},
		"method that returns nil": {
			err: &strayRuleSetError{
				split: func(*strayRuleSetError) error { return nil },
				errs:  []error{old, fail},
			},
			want: "bad seats",
		},
		"method that returns its receiver": {
			err: &strayRuleSetError{
				split: func(e *strayRuleSetError) error { return e },
				errs:  []error{old, fail},
			},
			want: "bad seats",
		},
		"method that returns only warnings": {
			err: &strayRuleSetError{
				split: func(*strayRuleSetError) error { return old },
				errs:  []error{old, fail},
			},
			want: "bad seats",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.WithoutWarnings(tc.err)
			if tc.want == "" {
				require.NoError(t, got)

				return
			}

			require.EqualError(t, got, tc.want)
			require.ErrorIs(t, got, fail)
			require.NotErrorIs(t, got, warn)
			require.NotErrorIs(t, got, old)
			assert.NotErrorAs(t, got, new(*plainRuleSetError))
			assert.NotErrorAs(t, got, new(*strayRuleSetError))
			assert.NotErrorAs(t, got, new(*blankRuleSetError))

			rs, ok := errors.AsType[*ruleSetError](got)
			require.Equal(t, tc.ruleSet, ok)

			if ok {
				assert.Equal(t, []error{fail}, rs.errs)
			}
		})
	}
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
		"a ValidateContext whose value call passes": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.DecodeInto(ctx, &heldSpecContextDoc{Spec: warningSpec{Seats: 3}})
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a ValidateContext whose value call fails": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.DecodeInto(ctx, &heldSpecContextDoc{Spec: mixedSpec{}})
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must be even",
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a ValidateContext that returns the warnings of a collector from its context": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.DecodeInto(ctx, &heldSpecHookDoc{Spec: warningSpec{Seats: 3}})
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a ValidateContext that returns the warnings of a failing call": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.DecodeInto(ctx, &heldSpecHookDoc{Spec: mixedSpec{}})
			},
			err:      "cafe.yaml:2:10: $.spec.seats: seats must be even",
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a value check that returns the warnings of a collector from its context": {
			run: func(_ *testing.T, ctx context.Context) error {
				var got plainCafeDoc

				return doc.DecodeInto(
					ctx,
					&got,
					niceyaml.WithValueCheck(func(ctx context.Context, _ *plainCafeDoc) error {
						warnings := niceyaml.NewWarnings()
						err := niceyaml.SelfValidateValue(warnings.Context(ctx), warningSpec{Seats: 4})

						return niceyaml.Rebase(errors.Join(err, warnings.Err()), paths.Current().Child("spec"))
					}),
				)
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a validator that returns the warnings of a collector from its context": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
					warnings := niceyaml.NewWarnings()
					err := niceyaml.SelfValidateValue(warnings.Context(ctx), warningSpec{Seats: 4})

					return fmt.Errorf(
						"spec: %w",
						niceyaml.Rebase(errors.Join(err, warnings.Err()), paths.Current().Child("spec")),
					)
				}))
			},
			warnings: "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
		},
		"a failing validator whose nested check returns a warning in a wrapper of a value type": {
			run: func(_ *testing.T, ctx context.Context) error {
				return doc.Validate(ctx, niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
					err := n.Validate(ctx, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
						return notedError{err: n.Bind(niceyaml.NewWarning("old layout")), notes: []string{"noted"}}
					}))
					if err != nil {
						return fmt.Errorf("nested check: %w", err)
					}

					return errors.New("boom")
				}))
			},
			err:      "cafe.yaml: boom",
			warnings: "noted: cafe.yaml: warning: old layout",
		},
		"a validator that returns a warning of the same error that its nested check warns at another node": {
			run: func(_ *testing.T, ctx context.Context) error {
				old := niceyaml.NewWarning("old layout")

				return doc.Validate(ctx, niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
					warnings := niceyaml.NewWarnings()
					err := n.Validate(
						warnings.Context(ctx),
						niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
							n.Warn(ctx, old)

							return nil
						}),
					)
					if err != nil {
						return fmt.Errorf("nested check: %w", err)
					}

					return niceyaml.Rebase(old, paths.Current().Child("extra"))
				}))
			},
			warnings: stringtest.JoinLF(
				"cafe.yaml: 2 warnings",
				"cafe.yaml:3:8: warning: $.extra: old layout",
				"cafe.yaml: warning: old layout",
			),
		},
		"a SelfValidateValue whose ValidateContext returns the warnings of a collector from its context": {
			run: func(_ *testing.T, ctx context.Context) error {
				return niceyaml.SelfValidateValue(ctx, heldSpecHookDoc{Spec: []warningSpec{{Seats: 3}, {Seats: 5}}})
			},
			warnings: stringtest.JoinLF(
				"2 warnings",
				"warning: $.spec[0].seats: fewer than 10 seats",
				"warning: $.spec[1].seats: fewer than 10 seats",
			),
		},
		"a ValidateContext that returns a warning": {
			run: func(_ *testing.T, ctx context.Context) error {
				var got contextCafeDoc

				return doc.DecodeInto(ctx, &got)
			},
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

	t.Run("a value check returns the warnings of a collector from its context as errors", func(t *testing.T) {
		t.Parallel()

		// The check returns each warning that it handed to its context, so
		// the decode does not report that warning beside the error.
		warnings := niceyaml.NewWarnings()

		var got plainCafeDoc

		err := doc.DecodeInto(warnings.Context(t.Context()), &got, niceyaml.WithValueCheck(
			func(ctx context.Context, _ *plainCafeDoc) error {
				held := niceyaml.NewWarnings()
				err := niceyaml.SelfValidateValue(held.Context(ctx), warningSpec{Seats: 4})

				return niceyaml.AsErrors(niceyaml.Rebase(errors.Join(err, held.Err()), paths.Current().Child("spec")))
			},
		))
		require.EqualError(t, err, "cafe.yaml:2:10: $.spec.seats: fewer than 10 seats")
		require.NoError(t, warnings.Err())
	})

	t.Run("a validator returns the warnings of a collector from its context as errors", func(t *testing.T) {
		t.Parallel()

		// The nested check binds its warning to the document, so the
		// errors the validator returns hold a copy of that binding.
		warnings := niceyaml.NewWarnings()

		err := doc.Validate(warnings.Context(t.Context()), niceyaml.ValidatorFunc(
			func(ctx context.Context, n *niceyaml.Node) error {
				held := niceyaml.NewWarnings()
				err := n.Validate(held.Context(ctx), oldSpec)
				if err != nil {
					return fmt.Errorf("nested check: %w", err)
				}

				return niceyaml.AsErrors(held.Err())
			},
		))
		require.EqualError(t, err, "cafe.yaml:1:1: $.spec~: old spec")
		require.NoError(t, warnings.Err())
	})

	t.Run("a parent validates after a child warns, and the decode passes", func(t *testing.T) {
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

	t.Run("a failure of a caller's type that holds a warning", func(t *testing.T) {
		t.Parallel()

		problems := func() []error {
			return []error{
				yamltest.ErrorAt("bad extra", "extra"),
				niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key())),
			}
		}

		// Only a type with a WithoutWarnings method keeps its type. Any
		// other type becomes the join of its errors, whatever its message,
		// so errors.As never finds a value that still holds the warning.
		tcs := map[string]struct {
			run  func(ctx context.Context) error
			err  string
			kept bool
		}{
			"aggregate with the message of a join": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&lintError{errs: problems()}))
				},
				err: "cafe.yaml:3:8: $.extra: bad extra",
			},
			"aggregate under a heading": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&plainRuleSetError{set: "net", errs: problems()}))
				},
				err: "cafe.yaml:3:8: $.extra: bad extra",
			},
			"wrapper that shows the text it wraps": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&yamltest.CheckError{Err: errors.Join(problems()...)}))
				},
				err: "cafe.yaml:3:8: $.extra: bad extra",
			},
			"aggregate with a WithoutWarnings method": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&ruleSetError{set: "net", errs: problems()}))
				},
				err:  "cafe.yaml: ruleset net\ncafe.yaml:3:8: $.extra: bad extra",
				kept: true,
			},
			"wrapper around an aggregate with a WithoutWarnings method": {
				run: func(ctx context.Context) error {
					rs := &ruleSetError{set: "net", errs: problems()}

					return doc.Validate(ctx, returns(fmt.Errorf("ctx: %w", rs)))
				},
				err:  "cafe.yaml: ctx: ruleset net\ncafe.yaml:3:8: $.extra: bad extra",
				kept: true,
			},
			"bound aggregate with a WithoutWarnings method": {
				// Node.Bind puts a SourceError on top, which does not split
				// itself, so the type is lost.
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
						return n.Bind(&ruleSetError{set: "net", errs: problems()})
					}))
				},
				err: "cafe.yaml:3:8: $.extra: bad extra",
			},
			"detached wrapper around bound problems": {
				// The copy that Detach builds of a wrapper of one %w verb
				// splits as the wrapper does, so it keeps its text.
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
						bound := make([]error, 0, len(problems()))
						for _, p := range problems() {
							bound = append(bound, n.Bind(p))
						}

						return niceyaml.Detach(fmt.Errorf("ctx: %w", errors.Join(bound...)))
					}))
				},
				err: "ctx: cafe.yaml:3:8: $.extra: bad extra",
			},
			"aggregate whose WithoutWarnings method returns nil": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&strayRuleSetError{
						split: func(*strayRuleSetError) error { return nil },
						errs:  problems(),
					}))
				},
				err: "cafe.yaml:3:8: $.extra: bad extra",
			},
			"aggregate whose WithoutWarnings method returns its receiver": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&strayRuleSetError{
						split: func(e *strayRuleSetError) error { return e },
						errs:  problems(),
					}))
				},
				err: "cafe.yaml:3:8: $.extra: bad extra",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				err := tc.run(warnings.Context(t.Context()))
				require.EqualError(t, err, tc.err)
				assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", warnings.Err().Error())

				for problem := range niceyaml.Problems(err) {
					assert.False(t, problem.Warning, problem.Message())
				}

				// The type that errors.As finds holds the problems of err.
				for _, target := range []any{
					new(*lintError), new(*plainRuleSetError), new(*strayRuleSetError), new(*yamltest.CheckError),
				} {
					assert.NotErrorAs(t, err, target)
				}

				rs, ok := errors.AsType[*ruleSetError](err)
				require.Equal(t, tc.kept, ok)

				if ok {
					assert.Equal(t, "net", rs.set)
					assert.Len(t, rs.errs, 1)
					assert.False(t, niceyaml.HasErrors(warnings.Err()))
				}
			})
		}
	})

	t.Run("a Validate method keeps the type of a failure that splits itself", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		var got ruleSetCafeDoc

		err := doc.DecodeInto(warnings.Context(t.Context()), &got)
		require.Error(t, err)

		rs, ok := errors.AsType[*ruleSetError](err)
		require.True(t, ok)
		assert.Equal(t, "net", rs.set)
		require.Len(t, rs.errs, 1)
		assert.True(t, niceyaml.HasErrors(rs))

		for problem := range niceyaml.Problems(err) {
			assert.False(t, problem.Warning, problem.Message())
		}

		assert.Equal(t, "cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats", warnings.Err().Error())
	})

	t.Run("a value check keeps the type of a failure that splits itself", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		var got checkedCafeDoc

		err := doc.DecodeInto(warnings.Context(t.Context()), &got,
			niceyaml.WithValueCheck(func(context.Context, *seatsOnly) error {
				return &ruleSetError{set: "net", errs: []error{
					niceyaml.NewWarning("few seats", niceyaml.AtPath(paths.Current().Child("seats"))),
					yamltest.ErrorAt("bad seats", "seats"),
				}}
			}))
		require.Error(t, err)

		rs, ok := errors.AsType[*ruleSetError](err)
		require.True(t, ok)
		require.Len(t, rs.errs, 1)

		for problem := range niceyaml.Problems(err) {
			assert.False(t, problem.Warning, problem.Message())
		}

		assert.Equal(t, "cafe.yaml:2:10: warning: $.spec.seats: few seats", warnings.Err().Error())
	})

	t.Run("each problem of a hook keeps the context of the wrapper above it", func(t *testing.T) {
		t.Parallel()

		badExtra := func() error { return yamltest.ErrorAt("bad extra", "extra") }
		badSeats := func() error { return yamltest.ErrorAt("bad seats", "spec", "seats") }
		badSpec := func() error { return yamltest.ErrorAt("bad spec", "spec") }
		oldSpec := func() error {
			return niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key()))
		}
		oldLayout := func() error { return niceyaml.NewWarning("old layout") }

		// A call takes the warnings of a hook out of the result of the
		// hook. A problem below a wrapper must keep the text of that
		// wrapper, whether the wrapper holds that problem alone, several
		// problems of one severity, or errors beside warnings.
		tcs := map[string]struct {
			run func(ctx context.Context) error
			// The context and the message of each problem of the error that
			// the call returns, and of each warning that the collector holds.
			err      []string
			warnings []string
		}{
			"validator wrapper around one warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w", oldSpec())))
				},
				warnings: []string{"[check] old spec"},
			},
			"validator wrapper around several warnings": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w", errors.Join(oldSpec(), oldLayout()))))
				},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"validator wrapper around an error and a warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w", errors.Join(badExtra(), oldSpec()))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec"},
			},
			"validator wrapper around several errors and several warnings": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badSeats(), oldSpec(), badExtra(), oldLayout()))))
				},
				err:      []string{"[check] bad seats", "[check] bad extra"},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"validator wrapper that adds text after an error and a warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("%w (see docs)", errors.Join(badExtra(), oldSpec()))))
				},
				err:      []string{"[(see docs)] bad extra"},
				warnings: []string{"[(see docs)] old spec"},
			},
			"validator wrappers around an error and around a wrapped warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badExtra(), fmt.Errorf("lint: %w", oldSpec())))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check: lint] old spec"},
			},
			"validator wrapper with a sentinel around an error and a warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("%w: %w",
						errors.New("bad cafe"), errors.Join(badExtra(), oldSpec()))))
				},
				err:      []string{"[bad cafe] bad extra"},
				warnings: []string{"[bad cafe] old spec"},
			},
			"validator wrapper of its own type around an error and a warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(&yamltest.CheckError{Err: errors.Join(badExtra(), oldSpec())}))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec"},
			},
			"validator wrapper that rewrites the text of an error and a warning": {
				// The text of the wrapper does not hold the text of the
				// problems, so its whole text is their context.
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(yamltest.RewriteError{Err: errors.Join(badExtra(), oldSpec())}))
				},
				err:      []string{"[rewritten] bad extra"},
				warnings: []string{"[rewritten] old spec"},
			},
			"validator summary over a wrapper around several errors": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(niceyaml.NewSummary("load failed",
						fmt.Errorf("check: %w", errors.Join(badSeats(), badExtra())), badSpec())))
				},
				err: []string{"[] bad spec", "[check] bad seats", "[check] bad extra"},
			},
			"validator summary over a wrapper around an error and a warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(niceyaml.NewSummary("load failed",
						fmt.Errorf("check: %w", errors.Join(badSeats(), oldSpec())), badSpec())))
				},
				err:      []string{"[] bad spec", "[check] bad seats"},
				warnings: []string{"[check] old spec"},
			},
			"validator wrapper around a summary over a wrapped error and a warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("load: %w", niceyaml.NewSummary("some problems",
						fmt.Errorf("check: %w", errors.Join(badSeats(), oldSpec())), badSpec()))))
				},
				err:      []string{"[load] bad spec", "[load: check] bad seats"},
				warnings: []string{"[load: check] old spec"},
			},
			"validator wrappers around an error and around a wrapped error and warning": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badExtra(), fmt.Errorf("lint: %w", errors.Join(badSeats(), oldSpec()))))))
				},
				err:      []string{"[check] bad extra", "[check: lint] bad seats"},
				warnings: []string{"[check: lint] old spec"},
			},
			"validator wrappers around errors and around a wrapper of several warnings": {
				// The call takes two wrappers away from the wrapper around
				// the warnings, and the text of the outer one stays in front.
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w", errors.Join(badExtra(),
						fmt.Errorf("lint: %w", errors.Join(badSeats(),
							fmt.Errorf("rule: %w", errors.Join(oldSpec(), oldLayout()))))))))
				},
				err:      []string{"[check] bad extra", "[check: lint] bad seats"},
				warnings: []string{"[check: lint: rule] old spec", "[check: lint: rule] old layout"},
			},
			"validator wrapper around an error and a join of warnings": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badExtra(), errors.Join(oldSpec(), oldLayout())))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"validator wrapper around an error and located warnings": {
				// The located Error does not have a row, so each warning
				// below it takes the text of the wrapper.
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w", errors.Join(badExtra(),
						niceyaml.Locate(errors.Join(oldLayout(), niceyaml.NewWarning("old plan")),
							niceyaml.AtPath(paths.Current().Child("spec")))))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old layout", "[check] old plan"},
			},
			"validator wrapper around an error and a wrapper without text around warnings": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badExtra(), fmt.Errorf("%w", errors.Join(oldSpec(), oldLayout()))))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"validator wrapper around an error and a binding of several warnings": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badExtra(), doc.Bind(errors.Join(oldSpec(), oldLayout()))))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"validator wrapper around an error and a binding of a summary of warnings": {
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w", errors.Join(badExtra(),
						doc.Bind(niceyaml.NewSummary("old cafe", oldSpec(), oldLayout()))))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"validator wrapper around an error and warnings that BindValue bound": {
				// The document places the warnings again, and they keep the
				// text of the wrapper.
				run: func(ctx context.Context) error {
					return doc.Validate(ctx, returns(fmt.Errorf("check: %w",
						errors.Join(badExtra(), niceyaml.BindValue(errors.Join(oldSpec(), oldLayout()))))))
				},
				err:      []string{"[check] bad extra"},
				warnings: []string{"[check] old spec", "[check] old layout"},
			},
			"Validate wrapper around one warning": {
				run: func(ctx context.Context) error {
					return niceyaml.SelfValidateValue(ctx, wrappedSpec{Seats: 4, Tables: 2})
				},
				warnings: []string{"[spec] patio not set"},
			},
			"Validate wrapper around an error and a warning": {
				run: func(ctx context.Context) error {
					return niceyaml.SelfValidateValue(ctx, wrappedSpec{Seats: 4})
				},
				err:      []string{"[spec] tables not set"},
				warnings: []string{"[spec] patio not set"},
			},
			"Validate wrapper around several errors and a warning": {
				run: func(ctx context.Context) error {
					return niceyaml.SelfValidateValue(ctx, wrappedSpec{})
				},
				err:      []string{"[spec] seats not set", "[spec] tables not set"},
				warnings: []string{"[spec] patio not set"},
			},
			"value check wrapper around an error and a warning": {
				run: func(ctx context.Context) error {
					var got checkedCafeDoc

					return doc.DecodeInto(ctx, &got, niceyaml.WithValueCheck(func(context.Context, *seatsOnly) error {
						return fmt.Errorf("check: %w", errors.Join(
							yamltest.ErrorAt("bad seats", "seats"),
							niceyaml.NewWarning("few seats", niceyaml.AtPath(paths.Current().Child("seats"))),
						))
					}))
				},
				err:      []string{"[check] bad seats"},
				warnings: []string{"[check] few seats"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				err := tc.run(warnings.Context(t.Context()))

				assert.ElementsMatch(t, tc.err, contextRows(err))
				assert.ElementsMatch(t, tc.warnings, heldContextRows(warnings))
				assert.ElementsMatch(t, tc.warnings, contextRows(warnings.Err()))

				// The report of the call lists each problem with the same
				// context.
				assert.ElementsMatch(t, slices.Concat(tc.err, tc.warnings), contextRows(warnings.Report(err)))
			})
		}
	})

	t.Run("the warnings of a hook keep one order beside an error", func(t *testing.T) {
		t.Parallel()

		menu := yamltest.FirstDocument(t, "seats: 2\nname: menu\n", niceyaml.WithName("menu.yaml"))

		fewSeats := func() error {
			return niceyaml.NewWarning("few seats", niceyaml.AtPath(paths.Current().Child("spec", "seats")))
		}
		oldSeats := func() error {
			return niceyaml.NewWarning("old seats", niceyaml.AtPath(paths.Current().Child("spec", "seats")))
		}
		oldSpec := func() error {
			return niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key()))
		}
		menuSeats := func() error {
			return menu.Bind(niceyaml.NewWarning("menu seats", niceyaml.AtPath(paths.Current().Child("seats"))))
		}
		menuName := func() error {
			return menu.Bind(niceyaml.NewWarning("menu name", niceyaml.AtPath(paths.Current().Child("name"))))
		}
		joined := func(beside []error, errs ...error) error {
			return errors.Join(slices.Concat(errs, beside)...)
		}
		rows := func(problems iter.Seq[niceyaml.ErrorTree]) []string {
			var out []string

			for problem := range problems {
				if problem.Warning {
					out = append(out, fmt.Sprintf("%s [%s] %s", problem.Name(), problem.Context(), problem.Message()))
				}
			}

			return out
		}

		// A call takes the warnings of a hook out of the result of the
		// hook, then binds and sorts them. The warnings below a heading
		// must stay below it, as they do in a result without an error.
		// Otherwise two warnings at one position swap rows, and two
		// sources swap places, when an error appears beside them.
		tcs := map[string]struct {
			// The result of the hook, with the errors beside its warnings.
			result func(beside ...error) error
			// The source, the context, and the message of each warning, in
			// the order that the collector lists them.
			want []string
		}{
			"wrapper around two warnings beside a warning at the position of one": {
				result: func(beside ...error) error {
					return joined(beside, fmt.Errorf("rule one: %w", errors.Join(fewSeats(), oldSpec())), oldSeats())
				},
				want: []string{
					"cafe.yaml [rule one] old spec",
					"cafe.yaml [] old seats",
					"cafe.yaml [rule one] few seats",
				},
			},
			"summary over two warnings beside a warning at the position of one": {
				result: func(beside ...error) error {
					return joined(beside, niceyaml.NewSummary("deprecations", fewSeats(), oldSpec()), oldSeats())
				},
				want: []string{
					"cafe.yaml [] old spec",
					"cafe.yaml [] old seats",
					"cafe.yaml [] few seats",
				},
			},
			"wrapper around the errors and a wrapper around two warnings": {
				result: func(beside ...error) error {
					return fmt.Errorf("check: %w", joined(beside,
						fmt.Errorf("rule one: %w", errors.Join(fewSeats(), oldSpec())), oldSeats()))
				},
				want: []string{
					"cafe.yaml [check: rule one] old spec",
					"cafe.yaml [check] old seats",
					"cafe.yaml [check: rule one] few seats",
				},
			},
			"wrapper around the errors and a summary over two warnings": {
				result: func(beside ...error) error {
					return fmt.Errorf("check: %w", joined(beside,
						niceyaml.NewSummary("deprecations", fewSeats(), oldSpec()), oldSeats()))
				},
				want: []string{
					"cafe.yaml [check] old spec",
					"cafe.yaml [check] old seats",
					"cafe.yaml [check] few seats",
				},
			},
			"summary over the warnings of two sources beside a warning of each": {
				result: func(beside ...error) error {
					return joined(
						beside,
						niceyaml.NewSummary("old seats", menuSeats(), oldSeats()),
						oldSpec(),
						menuName(),
					)
				},
				want: []string{
					"cafe.yaml [] old spec",
					"cafe.yaml [] old seats",
					"menu.yaml [] menu seats",
					"menu.yaml [] menu name",
				},
			},
			"summary bound in another source beside a warning of the document": {
				result: func(beside ...error) error {
					return joined(beside, menu.Bind(niceyaml.NewSummary(
						"old menu",
						niceyaml.NewWarning("menu layout"),
						niceyaml.NewWarning(
							"menu seats",
							niceyaml.AtPath(paths.Current().Child("seats")),
						),
					)), oldSeats())
				},
				want: []string{
					"cafe.yaml [] old seats",
					"menu.yaml [] menu seats",
					"menu.yaml [] menu layout",
				},
			},
		}

		besides := map[string][]error{
			"warnings alone":  nil,
			"beside an error": {yamltest.ErrorAt("bad extra", "extra")},
		}

		for name, tc := range tcs {
			for beside, errs := range besides {
				t.Run(name+" "+beside, func(t *testing.T) {
					t.Parallel()

					warnings := niceyaml.NewWarnings()

					err := doc.Validate(warnings.Context(t.Context()), returns(tc.result(errs...)))
					assert.Len(t, contextRows(err), len(errs))

					assert.Equal(t, tc.want, rows(warnings.All()))
					assert.Equal(t, tc.want, rows(niceyaml.Problems(warnings.Err())))
					assert.Equal(t, tc.want, rows(niceyaml.Problems(warnings.Report(err))))
				})
			}
		}
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

		// A wildcard path does not resolve to one node.
		deep := func() error {
			return niceyaml.NewWarning("old deep", niceyaml.AtPath(paths.Doc().Child("spec").ChildAll()))
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
					"all.yaml: document 1: warning: $.spec.*: old deep",
					"all.yaml: document 2: warning: $.spec.*: old deep",
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
					"all.yaml: document 2: warning: $.spec.*: old deep",
					"all.yaml: document 1: warning: $.spec.*: old deep",
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

	t.Run("a warning keeps one row for each line that it prints", func(t *testing.T) {
		t.Parallel()

		oldSpec := func() error {
			return niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key()))
		}
		oldExtra := func() error {
			return niceyaml.NewWarning("old extra", niceyaml.AtPath(paths.Current().Child("extra").Key()))
		}
		fewSeats := func() error {
			return niceyaml.NewWarning("few seats", niceyaml.AtPath(paths.Current().Child("spec").Child("seats")))
		}

		const (
			specRow      = "cafe.yaml:1:1: warning: $.spec~: old spec"
			lintSpecRow  = "cafe.yaml:1:1: warning: $.spec~: lint: old spec"
			styleSpecRow = "cafe.yaml:1:1: warning: $.spec~: style: old spec"
			seatsRow     = "cafe.yaml:2:10: warning: $.spec.seats: few seats"
			extraRow     = "cafe.yaml:3:1: warning: $.extra~: old extra"
			badExtraRow  = "cafe.yaml:3:8: $.extra: bad extra"
		)

		// A call that fails with the warning below a heading, beside an
		// error.
		failure := func() error {
			return doc.Bind(fmt.Errorf("lint: %w", errors.Join(oldSpec(), yamltest.ErrorAt("bad extra", "extra"))))
		}

		// The collector compares the line that each row prints. A wrapper
		// that holds a warning alone writes its text in that line, and a
		// wrapper that heads several warnings does not. The lines that the
		// collector prints thus must not repeat, and must not change with
		// the order in which the warnings reach it. A row that two headings
		// share keeps the context of the warning that came first.
		tcs := map[string]struct {
			first  error
			second error
			// The lines of the error of the collector, in either order.
			want []string
			// The lines of the report of the collector for failure, in
			// either order.
			report []string
			// The context and the message of each warning that the collector
			// holds after first and then second, and after second and then
			// first.
			held     []string
			reversed []string
		}{
			"two wrappers that each hold the warning alone": {
				first:  fmt.Errorf("lint: %w", oldSpec()),
				second: fmt.Errorf("style: %w", oldSpec()),
				want: []string{
					"cafe.yaml: 2 warnings",
					lintSpecRow,
					styleSpecRow,
				},
				report: []string{
					"cafe.yaml: 1 error, 3 warnings",
					specRow,
					lintSpecRow,
					styleSpecRow,
					badExtraRow,
				},
				held:     []string{"[lint] old spec", "[style] old spec"},
				reversed: []string{"[lint] old spec", "[style] old spec"},
			},
			"two wrappers that each head several warnings": {
				first:  fmt.Errorf("lint: %w", errors.Join(oldSpec(), oldExtra())),
				second: fmt.Errorf("style: %w", errors.Join(oldSpec(), fewSeats())),
				want: []string{
					"cafe.yaml: 3 warnings",
					specRow,
					seatsRow,
					extraRow,
				},
				report: []string{
					"cafe.yaml: 1 error, 3 warnings",
					specRow,
					seatsRow,
					extraRow,
					badExtraRow,
				},
				held:     []string{"[lint] old spec", "[style] few seats", "[lint] old extra"},
				reversed: []string{"[style] old spec", "[style] few seats", "[lint] old extra"},
			},
			"one wrapper alone and another that heads several warnings": {
				first:  fmt.Errorf("lint: %w", oldSpec()),
				second: fmt.Errorf("style: %w", errors.Join(oldSpec(), fewSeats())),
				want: []string{
					"cafe.yaml: 3 warnings",
					lintSpecRow,
					specRow,
					seatsRow,
				},
				report: []string{
					"cafe.yaml: 1 error, 3 warnings",
					specRow,
					lintSpecRow,
					seatsRow,
					badExtraRow,
				},
				held:     []string{"[lint] old spec", "[style] old spec", "[style] few seats"},
				reversed: []string{"[lint] old spec", "[style] old spec", "[style] few seats"},
			},
			"one text around the warning alone and over several": {
				first:  fmt.Errorf("lint: %w", oldSpec()),
				second: fmt.Errorf("lint: %w", errors.Join(oldSpec(), oldExtra())),
				want: []string{
					"cafe.yaml: 3 warnings",
					lintSpecRow,
					specRow,
					extraRow,
				},
				report: []string{
					"cafe.yaml: 1 error, 3 warnings",
					specRow,
					lintSpecRow,
					extraRow,
					badExtraRow,
				},
				held:     []string{"[lint] old spec", "[lint] old spec", "[lint] old extra"},
				reversed: []string{"[lint] old spec", "[lint] old spec", "[lint] old extra"},
			},
			"a warning below a heading and the same warning without a wrapper": {
				first:  fmt.Errorf("lint: %w", errors.Join(oldSpec(), oldExtra())),
				second: oldSpec(),
				want: []string{
					"cafe.yaml: 2 warnings",
					specRow,
					extraRow,
				},
				report: []string{
					"cafe.yaml: 1 error, 2 warnings",
					specRow,
					extraRow,
					badExtraRow,
				},
				held:     []string{"[lint] old spec", "[lint] old extra"},
				reversed: []string{"[] old spec", "[lint] old extra"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				orders := map[string]struct {
					warn []error
					held []string
				}{
					"first then second": {warn: []error{tc.first, tc.second}, held: tc.held},
					"second then first": {warn: []error{tc.second, tc.first}, held: tc.reversed},
				}

				for order, o := range orders {
					warnings := niceyaml.NewWarnings()
					ctx := warnings.Context(t.Context())

					for _, err := range o.warn {
						doc.Warn(ctx, err)
					}

					got := warnings.Err()
					require.Error(t, got, order)

					// Two rows at one position keep the order in which they
					// reached the collector, so the test compares the lines
					// without that order.
					assert.ElementsMatch(t, tc.want, strings.Split(got.Error(), "\n"), order)
					assert.ElementsMatch(t, o.held, heldContextRows(warnings), order)
					assert.ElementsMatch(t, o.held, contextRows(got), order)

					report := warnings.Report(failure())
					require.Error(t, report, order)
					assert.ElementsMatch(t, tc.report, strings.Split(report.Error(), "\n"), order)
				}
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
		"Node.DecodeInto runs a ValidateContext method": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.DecodeInto(ctx, &selfHookedDoc{hook: hook})
			},
		},
		"Node.SelfValidate runs a ValidateContext method": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return doc.SelfValidate(ctx, &selfHookedDoc{hook: hook})
			},
		},
		"SelfValidateValue runs a ValidateContext method": {
			call: func(ctx context.Context, hook func(context.Context) error) error {
				return niceyaml.SelfValidateValue(ctx, selfHookedDoc{hook: hook})
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

	// A wildcard path does not resolve to one node.
	oldDeep := niceyaml.NewWarning("old deep", niceyaml.AtPath(paths.Doc().Child("extra").ChildAll()))

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
		"a copy of a held warning below a wrapper of several problems shows once": {
			// The wrapper heads the problems, so the report leaves it out,
			// and the held warning still counts as the copy below it.
			err:  doc.Bind(fmt.Errorf("lint: %w", errors.Join(oldSpec, badExtra))),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"cafe.yaml: 1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
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
				"cafe.yaml:1:1: warning: $.spec: $.extra.*: old deep",
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
				"cafe.yaml:1:1: warning: $.spec: $.extra.*: old deep",
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
		"a wrapped error that no source bound keeps the text of its wrapper": {
			// The heading binds the error outside any document, so its row
			// names the path and does not have a position.
			err:  fmt.Errorf("load: %w", badExtra),
			warn: []error{oldSpec},
			is:   fs.ErrPermission,
			want: stringtest.JoinLF(
				"1 error, 1 warning",
				"cafe.yaml:1:1: warning: $.spec~: old spec",
				"$.extra: load: permission denied",
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

	t.Run("each problem keeps the context of the wrapper above it", func(t *testing.T) {
		t.Parallel()

		// The report leaves out the heading that a wrapper makes over
		// several problems. Each problem must still report the text of
		// that wrapper, as it does in the error and for one problem alone.
		tcs := map[string]struct {
			err error
			// The context and the message of each problem of the report.
			want []string
			// The text of the report, which holds the text of a wrapper
			// only in the row that the wrapper shares with one problem.
			text string
			// A problem of err that the report matches as err does.
			is error
		}{
			"wrapper around one bound problem": {
				err:  fmt.Errorf("load: %w", doc.Bind(badSeats)),
				is:   badSeats,
				want: []string{"[] old spec", "[load] bad seats"},
				text: stringtest.JoinLF(
					"cafe.yaml: 1 error, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"cafe.yaml:2:10: $.spec.seats: load: bad seats",
				),
			},
			"wrapper around several bound problems": {
				err:  fmt.Errorf("load: %w", doc.Bind(errors.Join(badSeats, badExtra))),
				is:   badSeats,
				want: []string{"[] old spec", "[load] bad seats", "[load] permission denied"},
				text: stringtest.JoinLF(
					"cafe.yaml: 2 errors, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"cafe.yaml:2:10: $.spec.seats: bad seats",
					"cafe.yaml:3:8: $.extra: permission denied",
				),
			},
			"binding of a wrapper around several problems": {
				err:  doc.Bind(fmt.Errorf("load: %w", errors.Join(badSeats, badExtra))),
				is:   badSeats,
				want: []string{"[] old spec", "[load] bad seats", "[load] permission denied"},
				text: stringtest.JoinLF(
					"cafe.yaml: 2 errors, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"cafe.yaml:2:10: $.spec.seats: bad seats",
					"cafe.yaml:3:8: $.extra: permission denied",
				),
			},
			"wrappers around a heading and around a problem below it": {
				err: fmt.Errorf("%w (see docs)", doc.Bind(niceyaml.NewSummary("some problems",
					fmt.Errorf("decode seats: %w", badSeats), badExtra,
				))),
				is:   badSeats,
				want: []string{"[] old spec", "[decode seats: (see docs)] bad seats", "[(see docs)] permission denied"},
				text: stringtest.JoinLF(
					"cafe.yaml: 2 errors, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"cafe.yaml:2:10: $.spec.seats: decode seats: bad seats",
					"cafe.yaml:3:8: $.extra: permission denied",
				),
			},
			"wrapper around several problems that no source bound": {
				err:  fmt.Errorf("load: %w", errors.Join(badSeats, badExtra)),
				is:   badSeats,
				want: []string{"[] old spec", "[load] bad seats", "[load] permission denied"},
				text: stringtest.JoinLF(
					"2 errors, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"$.spec.seats: bad seats",
					"$.extra: permission denied",
				),
			},
			"wrapper around several problems that BindValue bound": {
				err:  fmt.Errorf("load: %w", niceyaml.BindValue(errors.Join(badSeats, badExtra))),
				is:   badSeats,
				want: []string{"[] old spec", "[load] bad seats", "[load] permission denied"},
				text: stringtest.JoinLF(
					"2 errors, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"$.spec.seats: bad seats",
					"$.extra: permission denied",
				),
			},
			"wrapper around several errors without a location": {
				err:  fmt.Errorf("load: %w", errors.Join(fs.ErrPermission, fs.ErrNotExist)),
				is:   fs.ErrNotExist,
				want: []string{"[] old spec", "[load] permission denied", "[load] file does not exist"},
				text: stringtest.JoinLF(
					"2 errors, 1 warning",
					"cafe.yaml:1:1: warning: $.spec~: old spec",
					"permission denied",
					"file does not exist",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				doc.Warn(warnings.Context(t.Context()), oldSpec)

				got := warnings.Report(tc.err)
				require.Error(t, got)

				assert.Equal(t, tc.want, contextRows(got))

				assert.Equal(t, tc.text, got.Error())

				// Each problem reads in the report as it reads in the error.
				// A document that binds the report later places each
				// problem that no document bound, and keeps its context.
				assert.ElementsMatch(t, tc.want[1:], contextRows(tc.err))
				assert.ElementsMatch(t, tc.want, contextRows(doc.Bind(got)))

				// So do a detached copy of the report and the copy that
				// AsErrors returns for it.
				assert.Equal(t, tc.want, contextRows(niceyaml.Detach(got)))
				assert.Equal(t, tc.want, contextRows(niceyaml.AsErrors(got)))
				require.ErrorIs(t, got, tc.is)
			})
		}
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

		// A wildcard path does not resolve to one node.
		deep := func() error {
			return niceyaml.NewWarning("old deep", niceyaml.AtPath(paths.Doc().Child("spec").ChildAll()))
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
					"all.yaml: document 2: warning: $.spec.*: old deep",
					"all.yaml: document 1: warning: $.spec.*: old deep",
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
					"all.yaml: document 2: warning: $.spec.*: old deep",
					"all.yaml: document 1: warning: $.spec.*: old deep",
				),
			},
			"not beside a detached copy of it in the same document": {
				held: func(ctx context.Context) { docs[1].Warn(ctx, deep()) },
				err:  niceyaml.Detach(errors.Join(docs[1].Bind(deep()), bad())),
				want: stringtest.JoinLF(
					"1 error, 1 warning",
					"all.yaml:3:7: $.spec: bad spec",
					"all.yaml: document 2: warning: $.spec.*: old deep",
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
		// the binding inside it is an error. The text of the wrapper is
		// the context of the problem and stays out of its message.
		got := warnings.Err()
		require.Error(t, got)
		assert.Equal(t, "check: menu.yaml:1:1: $.items: odd", got.Error())

		problem := yamltest.OnlyProblem(t, got)
		assert.True(t, problem.Warning)
		assert.Equal(t, "menu.yaml:1:1: warning: $.items: check: odd", problem.Label())
		assert.Equal(t, "odd", problem.Message())
		assert.Equal(t, "check", problem.Context())
	})

	t.Run("each warning keeps the context of the wrapper above it", func(t *testing.T) {
		t.Parallel()

		oddPrice := niceyaml.NewWarning("odd price", niceyaml.AtPath(paths.Current().Child("price")))
		oddItem := niceyaml.NewWarning("odd item")

		// The collector holds each warning apart from the heading that a
		// wrapper makes over several. Each warning must still report the
		// text of that wrapper, as one warning alone does.
		tcs := map[string]struct {
			warn error
			want []string
		}{
			"wrapper around one warning": {
				warn: fmt.Errorf("check: %w", oddPrice),
				want: []string{"[check] odd price"},
			},
			"wrapper around several warnings": {
				warn: fmt.Errorf("check: %w", errors.Join(oddPrice, oddItem)),
				want: []string{"[check] odd price", "[check] odd item"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				item.Warn(warnings.Context(t.Context()), tc.warn)

				assert.ElementsMatch(t, tc.want, heldContextRows(warnings))
				assert.ElementsMatch(t, tc.want, contextRows(warnings.Err()))
			})
		}
	})

	t.Run("a located warning leaves the label out of the line of a binding", func(t *testing.T) {
		t.Parallel()

		included := niceyaml.NewSourceFromString("key: 1\n", niceyaml.WithName("inc.yaml"))
		atPrice := niceyaml.AtPath(paths.Current().Child("price"))
		old := func() error { return included.Bind(niceyaml.NewWarning("old")) }

		// The warning keeps its location above the binding of another
		// source, so its message holds the line of that binding. Each row
		// reports the severity apart from the message, whether or not the
		// wrapper kept the line of the binding in its text.
		tcs := map[string]struct {
			warn error
			want string
		}{
			"wrapper that keeps the line": {
				warn: niceyaml.Invalid(fmt.Errorf("check: %w", old()), atPrice),
				want: "[check] inc.yaml: old",
			},
			"wrapper that rewrites the line": {
				warn: niceyaml.Invalid(rewrittenError{msg: "config is invalid", err: old()}, atPrice),
				want: "[config is invalid] inc.yaml: old",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				warnings := niceyaml.NewWarnings()

				item.Warn(warnings.Context(t.Context()), tc.warn)

				assert.Equal(t, []string{tc.want}, heldContextRows(warnings))
				assert.Equal(t, []string{tc.want}, contextRows(warnings.Err()))

				for warning := range warnings.All() {
					assert.True(t, warning.Warning)
				}
			})
		}
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
