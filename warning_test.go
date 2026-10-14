package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
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

// collect returns a context whose warning handler records each call.
func collect(ctx context.Context) (context.Context, func() []error) {
	var (
		mu    sync.Mutex
		calls []error
	)

	ctx = niceyaml.WithWarningHandler(ctx, func(w error) {
		mu.Lock()
		defer mu.Unlock()

		calls = append(calls, w)
	})

	return ctx, func() []error {
		mu.Lock()
		defer mu.Unlock()

		return calls
	}
}

// onlyWarning returns the one warning that calls has recorded. The test
// fails unless calls holds exactly one.
func onlyWarning(tb testing.TB, calls func() []error) error {
	tb.Helper()

	got := calls()
	require.Len(tb, got, 1)

	return got[0]
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
			err:     fmt.Errorf("load: %w", doc.Bind(niceyaml.NewWarning("old layout"))),
			want:    "load: cafe.yaml: warning: old layout",
			message: "old layout",
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
		"nil":                         {err: nil, want: false},
		"nil Error pointer":           {err: nilError, want: false},
		"nil SourceError pointer":     {err: nilSourceError, want: false},
		"a warning":                   {err: warning, want: false},
		"a join of warnings":          {err: errors.Join(warning, niceyaml.NewWarning("older")), want: false},
		"an error":                    {err: niceyaml.NewError("bad"), want: true},
		"a plain error":               {err: errors.New("bad"), want: true},
		"a join of both":              {err: errors.Join(warning, errors.New("bad")), want: true},
		"an error with no problem":    {err: niceyaml.NewError(""), want: true},
		"a warning made an error":     {err: niceyaml.AsErrors(warning), want: true},
		"a wrapper with a warning":    {err: fmt.Errorf("%w; %w", fs.ErrNotExist, warning), want: false},
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
			err:  errors.Join(niceyaml.Place(niceyaml.NewWarning("x")), niceyaml.NewError("bad")),
			want: true,
		},
		"a warning that declares nothing answers for itself": {
			err:  niceyaml.Place(niceyaml.AsErrors(fs.ErrPermission)),
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

	require.NoError(t, niceyaml.AsErrors(nil))

	tcs := map[string]struct {
		err  error
		want string
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.AsErrors(tc.err)
			assert.Equal(t, tc.want, got.Error())
			assert.True(t, niceyaml.HasErrors(got))

			for problem := range niceyaml.Problems(got) {
				assert.False(t, problem.Warning)
			}
		})
	}
}

func TestAsErrors_Tally(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, warningCafe, niceyaml.WithName("cafe.yaml"))

	_, err := doc.Decode[warningSpec](t.Context(),
		niceyaml.WithValidator(warnAtKey("legacyPricing", `deprecated field "legacyPricing"`)),
		niceyaml.WithDisallowUnknownFields(true))
	require.Error(t, err)

	assert.Equal(t, stringtest.JoinLF(
		"cafe.yaml: 3 errors, 1 warning",
		`cafe.yaml:2:1: $.curency~: unknown field "curency"`,
		`cafe.yaml:3:1: $.legacyPricing~: unknown field "legacyPricing"`,
		`cafe.yaml:3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
		`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
	), err.Error())

	assert.Equal(t, stringtest.JoinLF(
		"cafe.yaml: 4 errors",
		`cafe.yaml:2:1: $.curency~: unknown field "curency"`,
		`cafe.yaml:3:1: $.legacyPricing~: unknown field "legacyPricing"`,
		`cafe.yaml:3:1: $.legacyPricing~: deprecated field "legacyPricing"`,
		`cafe.yaml:4:1: $.wifi~: unknown field "wifi"`,
	), niceyaml.AsErrors(err).Error())
}

func TestWithWarningHandler(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  seats: 4",
		"extra: x",
	), niceyaml.WithName("cafe.yaml"))

	warnExtra := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning("extra is unused", niceyaml.AtPath(paths.Current().Child("extra"))))

		return nil
	})
	oldSpec := warnAtKey("spec", "old spec")

	t.Run("a passing decode hands its warnings over once", func(t *testing.T) {
		t.Parallel()

		ctx, calls := collect(t.Context())

		ran := false

		got, err := doc.Decode[warningCafeDoc](ctx, niceyaml.WithValidator(warnExtra))
		require.NoError(t, err)
		assert.Equal(t, 4, got.Spec.Seats)

		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml: 2 warnings",
			"cafe.yaml:2:10: warning: $.spec.seats: fewer than 10 seats",
			"cafe.yaml:3:8: warning: $.extra: extra is unused",
		), onlyWarning(t, calls).Error())

		// A Validate whose values below it warned still runs.
		var parent warningCafeDoc

		parent.Ran = &ran
		require.NoError(t, doc.DecodeInto(t.Context(), &parent))
		assert.True(t, ran)
	})

	t.Run("a failing decode returns its warnings in its error", func(t *testing.T) {
		t.Parallel()

		failing := yamltest.FirstDocument(t, stringtest.LinesLF(
			"spec:",
			"  seats: -1",
			"extra: x",
		), niceyaml.WithName("cafe.yaml"))
		ctx, calls := collect(t.Context())

		_, err := failing.Decode[warningCafeDoc](ctx, niceyaml.WithValidator(warnExtra))
		require.Error(t, err)
		assert.Empty(t, calls())

		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml: 1 error, 1 warning",
			"cafe.yaml:2:10: $.spec.seats: seats must not be negative",
			"cafe.yaml:3:8: warning: $.extra: extra is unused",
		), err.Error())
	})

	t.Run("a call without a handler drops its warnings", func(t *testing.T) {
		t.Parallel()

		_, err := doc.Decode[warningCafeDoc](t.Context())
		require.NoError(t, err)
	})

	t.Run("a validator result of warnings alone passes", func(t *testing.T) {
		t.Parallel()

		ctx, calls := collect(t.Context())
		returns := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return niceyaml.NewWarning("old layout")
		})

		ranAfter := false
		after := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			ranAfter = true

			return nil
		})

		require.NoError(t, doc.Validate(ctx, niceyaml.ChainValidator(returns, after)))
		assert.True(t, ranAfter)
		assert.Equal(t, "cafe.yaml: warning: old layout", onlyWarning(t, calls).Error())
	})

	t.Run("each validator of a MultiValidator keeps its warnings", func(t *testing.T) {
		t.Parallel()

		ctx, calls := collect(t.Context())
		failing := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return yamltest.ErrorAt("bad extra", "extra")
		})

		err := doc.Validate(ctx, niceyaml.MultiValidator(oldSpec, failing))
		require.Error(t, err)
		assert.Empty(t, calls())
		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml: 1 error, 1 warning",
			"cafe.yaml:1:1: warning: $.spec~: old spec",
			"cafe.yaml:3:8: $.extra: bad extra",
		), err.Error())
	})

	t.Run("one warning that two checks report shows once", func(t *testing.T) {
		t.Parallel()

		ctx, calls := collect(t.Context())

		require.NoError(t, doc.Validate(ctx, niceyaml.MultiValidator(oldSpec, oldSpec)))
		assert.Equal(t, "cafe.yaml:1:1: warning: $.spec~: old spec", onlyWarning(t, calls).Error())
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

			ctx, calls := collect(t.Context())

			item.Warn(ctx, tc.warn)

			got := onlyWarning(t, calls)
			assert.Equal(t, tc.want, got.Error())
			assert.False(t, niceyaml.HasErrors(got))
		})
	}

	t.Run("nil warns nothing", func(t *testing.T) {
		t.Parallel()

		ctx, calls := collect(t.Context())

		item.Warn(ctx, nil)
		assert.Empty(t, calls())
	})
}

func TestSelfValidateValue_Warnings(t *testing.T) {
	t.Parallel()

	ctx, calls := collect(t.Context())

	require.NoError(t, niceyaml.SelfValidateValue(ctx, warningCafeDoc{Spec: warningSpec{Seats: 3}}))
	assert.Equal(t, "warning: $.spec.seats: fewer than 10 seats", onlyWarning(t, calls).Error())

	err := niceyaml.SelfValidateValue(ctx, warningSpec{Seats: -1})
	require.Error(t, err)
	assert.Equal(t, "$.seats: seats must not be negative", err.Error())
}

func TestSource_ValidateDocuments_Warnings(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"spec: 1",
		"---",
		"spec: 2",
		"extra: 3",
	), niceyaml.WithName("all.yaml"))
	failing := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning("old spec", niceyaml.AtPath(paths.Current().Child("spec").Key())))

		if n.DocumentIndex() == 1 {
			return yamltest.ErrorAt("bad extra", "extra")
		}

		return nil
	})

	err := source.ValidateDocuments(t.Context(), failing)
	require.Error(t, err)
	assert.Equal(t, stringtest.JoinLF(
		"all.yaml: 1 error, 2 warnings",
		"all.yaml:1:1: warning: $.spec~: old spec",
		"all.yaml:3:1: warning: $.spec~: old spec",
		"all.yaml:4:8: $.extra: bad extra",
	), err.Error())
}

func TestNewErrorReport_RepeatedWarnings(t *testing.T) {
	t.Parallel()

	newDoc := func(name string) *niceyaml.Node {
		return yamltest.FirstDocument(t, "apiVersion: v1alpha1\nkind: Hours\n", niceyaml.WithName(name))
	}

	deprecated := func(doc *niceyaml.Node) error {
		return doc.Bind(niceyaml.NewWarning(`deprecated version "v1alpha1"`,
			niceyaml.AtPath(paths.Doc().Child("apiVersion")),
			niceyaml.WithDetails(errors.New("cafe 4.0 removes v1alpha1"))))
	}

	err := errors.Join(deprecated(newDoc("hours-01.yaml")), deprecated(newDoc("hours-02.yaml")))

	assert.Equal(t, stringtest.JoinLF(
		`|-- hours-01.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
		"|   `-- cafe 4.0 removes v1alpha1",
		"`-- hours-02.yaml:1:13: warning: $.apiVersion: deprecated version \"v1alpha1\"",
		"",
		"hours-01.yaml",
		"   1 | apiVersion: v1alpha1",
		`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
		"   2 | kind: Hours",
	), niceyaml.FormatError(err))

	// The tree of the error keeps every detail.
	tree := niceyaml.NewErrorTree(err)
	require.Len(t, tree.Children, 2)
	assert.Len(t, tree.Children[1].Children, 1)
	assert.True(t, tree.Warning)
}
