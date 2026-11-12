package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"strings"
	"testing"
	"weak"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
)

// detachedRows returns what a report reads from each node of the tree of
// err, one line per node. A test compares the rows of an error with those
// of its detached copy.
func detachedRows(err error) []string {
	var rows []string

	for node := range niceyaml.NewErrorTree(err).All() {
		path, hasPath := node.Path()
		pos, hasPos := node.Position()
		rng, hasRange := node.Range()
		doc, hasDoc := node.DocumentIndex()

		rows = append(rows, fmt.Sprintf(
			"%q msg=%q path=%v,%t pos=%v,%t range=%v,%t doc=%d,%t name=%q file=%q invalid=%t warning=%t detail=%t",
			node.Label(), node.Message(), path, hasPath, pos, hasPos, rng, hasRange, doc, hasDoc,
			node.Name(), node.Bound.FilePath(), node.IsInvalid(), node.Warning, node.Detail,
		))
	}

	return rows
}

var (
	// ReferenceSource is the source that [lookupValue] binds its error in.
	referenceSource = niceyaml.NewSourceFromString("ref: 1\n", niceyaml.WithName("ref.yaml"))

	// WrappingValidator wraps a warning at `a` and an error at `b` in one
	// wrapper. The call that runs it splits the wrapper, so the call
	// returns the error alone under the message of the wrapper.
	wrappingValidator = niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		return fmt.Errorf("check: %w", errors.Join(
			niceyaml.NewWarning("old a", niceyaml.AtPath(paths.Doc().Child("a"))),
			n.Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b")))),
		))
	})
)

// lookupValue fails to decode with an error bound in [referenceSource],
// as a value that looks itself up in another file does.
type lookupValue struct{}

func (*lookupValue) UnmarshalYAML(func(any) error) error {
	return fmt.Errorf("lookup: %w", referenceSource.Bind(yamltest.ErrorAt("missing", "ref")))
}

// requireDetaches detaches err with opts and requires the result to read
// and render as err does with opts. It renders the result with other
// options too, which change nothing for it. It returns the result.
func requireDetaches(t *testing.T, err error, opts ...niceyaml.ExcerptOption) error {
	t.Helper()

	detached := niceyaml.Detach(err, opts...)
	require.Error(t, detached)

	other := []niceyaml.ExcerptOption{
		niceyaml.WithContextLines(7),
		niceyaml.WithExcerptWidth(3),
		niceyaml.WithExcerptMarks(1),
	}

	require.EqualError(t, detached, err.Error())
	assert.Equal(t, niceyaml.FormatError(err, opts...), niceyaml.FormatError(detached, opts...))
	assert.Equal(t, niceyaml.FormatError(err, opts...), niceyaml.FormatError(detached, other...))
	assert.Equal(t, detachedRows(err), detachedRows(detached))
	assert.Equal(t, niceyaml.IsInvalid(err), niceyaml.IsInvalid(detached))
	assert.Equal(t, niceyaml.HasErrors(err), niceyaml.HasErrors(detached))

	// The styles of the XML printer name the kind of each token, and the
	// default printer draws the gutter and the colors.
	for _, p := range []*printer.Printer{yamltest.NewXMLPrinter(), printer.New(printer.WithWrapWidth(60))} {
		assert.Equal(t, p.PrintError(err, opts...), p.PrintError(detached, other...))
	}

	live := slices.Collect(niceyaml.Bindings(err))
	kept := slices.Collect(niceyaml.Bindings(detached))
	require.Len(t, kept, len(live))

	for i, bound := range kept {
		assert.Equal(t, live[i].LogValue().String(), bound.LogValue().String())

		// A binding renders alone as it did in err, whatever the other
		// bindings of err mark on its lines.
		assert.Equal(t, niceyaml.FormatError(live[i], opts...), niceyaml.FormatError(bound, other...))

		// The %+v verb renders at the default options, so it matches err
		// when opts set none.
		if len(opts) == 0 {
			assert.Equal(t, fmt.Sprintf("%+v", live[i]), fmt.Sprintf("%+v", bound))
		}
	}

	for node := range niceyaml.NewErrorTree(detached).All() {
		if node.Bound == nil {
			continue
		}

		assert.Nil(t, node.Bound.Source(), node.Label())

		_, ok := node.Bound.Node()
		assert.False(t, ok, node.Label())

		_, ok = node.Bound.Document()
		assert.False(t, ok, node.Label())
	}

	return detached //nolint:wrapcheck // The test reads the detached error itself.
}

func TestDetach(t *testing.T) {
	t.Parallel()

	cafe := niceyaml.NewSourceFromString(stringtest.Input(`
		# The menu of the cafe.
		name: corner cafe
		owner: &owner
		  name: Ada
		manager: *owner
		hours:
		  open: 7
		  close: 19
		notes: |
		  Closed on holidays.
		  Cash only.
		menu:
		  - coffee
		  - tea
		  - cake
		  - scone
		  - bagel
		  - muffin
		  - toast
		  - soup
		  - salad
		wifi: false
	`), niceyaml.WithName("cafe.yaml"), niceyaml.WithFilePath("/srv/cafe.yaml"))
	doc := yamltest.Document(t, cafe)

	other := niceyaml.NewSourceFromString("port: 80\nhost: example.com\n", niceyaml.WithName("other.yaml"))
	otherDoc := yamltest.Document(t, other)

	several := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n---\nc: 3\n", niceyaml.WithName("many.yaml"))
	docs := yamltest.AllDocuments(t, several, 3)

	secret := niceyaml.NewSourceFromString(
		"token: abc\n",
		niceyaml.WithName("secret.yaml"),
		niceyaml.WithExcerpts(false),
	)
	secretDoc := yamltest.Document(t, secret)

	long := niceyaml.NewSourceFromString(
		"key: "+strings.Repeat("x", 300)+"\nnext: "+strings.Repeat("y", 100)+"z\n",
		niceyaml.WithName("long.yaml"),
	)
	longDoc := yamltest.Document(t, long)

	blank := yamltest.FirstDocument(
		t,
		"a: 1\n\n  \nb: |\n  x\n\n  y\n# end\nc: 2\n\n   ",
		niceyaml.WithName("blank.yaml"),
	)

	// Every item of one line fails, as the items of a minified document do.
	flowDoc := yamltest.FirstDocument(
		t,
		"tags: [a, b, c, d, e, f, g, h, i, j, k, l]\nnote: x\n",
		niceyaml.WithName("flow.yaml"),
	)

	tagErrs := make([]error, 0, 12)
	for i := range 12 {
		tagErrs = append(tagErrs, niceyaml.NewError("bad tag", niceyaml.AtPath(paths.Current().Child("tags").Index(i))))
	}

	manyOnOneLine := flowDoc.Bind(niceyaml.NewSummary("12 violations", tagErrs...))

	// The last item of the line runs on to the five lines after it. The
	// errors name that item first, and its column comes last.
	spanDoc := yamltest.FirstDocument(
		t,
		"tags: [a, b, c, d, e, f, g, h, i, j, k, \"x\n  y\n  z\n  w\n  v\n  u\"]\nnote: x\nmore: y\nlast: z\nend: w\n",
		niceyaml.WithName("span.yaml"),
	)

	spanErrs := slices.Concat(tagErrs[11:], tagErrs[:11])

	spanBindings := make([]error, 0, len(spanErrs))
	for _, tagErr := range spanErrs {
		spanBindings = append(spanBindings, spanDoc.Bind(tagErr))
	}

	longItemOfMany := spanDoc.Bind(niceyaml.NewSummary("12 violations", spanErrs...))
	longItemOfBindings := errors.Join(spanBindings...)

	_, syntaxErr := niceyaml.NewSourceFromString("a: [1, 2\nb: 3\n", niceyaml.WithName("bad.yaml")).File()
	require.Error(t, syntaxErr)

	type cafeConfig struct {
		Hours struct {
			Open []string `yaml:"open"`
		} `yaml:"hours"`
		Wifi int `yaml:"wifi"`
	}

	_, decodeErr := doc.Decode[cafeConfig](t.Context())
	require.Error(t, decodeErr)

	type lookupConfig struct {
		Name lookupValue `yaml:"name"`
	}

	_, lookupErr := doc.Decode[lookupConfig](t.Context())
	require.Error(t, lookupErr)

	pair := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("pair.yaml"))

	validateErr := pair.ValidateDocuments(t.Context(), wrappingValidator)
	require.Error(t, validateErr)

	// The report of a collector holds the warning the validation split
	// off, beside its error. Detach takes the report, so the warning and
	// the error share what Detach keeps of their source.
	warnings := niceyaml.NewWarnings()
	reportErr := warnings.Report(pair.ValidateDocuments(warnings.Context(t.Context()), wrappingValidator))
	require.Error(t, reportErr)

	at := func(msg string, keys ...string) error {
		return doc.Bind(yamltest.ErrorAt(msg, keys...))
	}

	tcs := map[string]struct {
		err  error
		opts []niceyaml.ExcerptOption
	}{
		"binding at a path": {
			err: at("too early", "hours", "open"),
		},
		"syntax error": {
			err: syntaxErr,
		},
		"decode error": {
			err: decodeErr,
		},
		"unmarshaler that returns a binding": {
			err: lookupErr,
		},
		"validator that wraps a warning and an error": {
			err: validateErr,
		},
		"report of a collector": {
			err: reportErr,
		},
		"summary of violations": {
			err: doc.Bind(niceyaml.NewSummary("3 violations",
				yamltest.ErrorAt("too early", "hours", "open"),
				yamltest.ErrorAt("not served", "menu", "3"),
				yamltest.ErrorAt("too long", "notes"),
			)),
		},
		"locations far apart keep separate hunks": {
			err: doc.Bind(niceyaml.NewSummary("2 violations",
				yamltest.ErrorAt("bad name", "name"),
				yamltest.ErrorAt("bad wifi", "wifi"),
			)),
		},
		"anchor and alias": {
			err: doc.Bind(niceyaml.NewSummary("2 violations",
				yamltest.ErrorAt("owner", "owner", "name"),
				niceyaml.NewError("manager", niceyaml.AtPath(paths.Doc().Child("manager"))),
			)),
		},
		"range across lines": {
			err: doc.Bind(niceyaml.NewError("block", niceyaml.AtRange(
				position.NewRange(position.New(9, 2), position.New(11, 0)),
			))),
		},
		"position past the end of a line": {
			err: doc.Bind(niceyaml.NewError("past", niceyaml.AtPosition(position.New(1, 40)))),
		},
		"warning beside an error": {
			err: doc.Bind(errors.Join(
				niceyaml.NewWarning("deprecated", niceyaml.AtPath(paths.Doc().Child("wifi"))),
				yamltest.ErrorAt("too early", "hours", "open"),
			)),
		},
		"detail in another source": {
			err: doc.Bind(niceyaml.NewError("conflict",
				niceyaml.AtPath(paths.Doc().Child("hours")),
				niceyaml.WithDetails(otherDoc.Bind(yamltest.ErrorAt("declared here", "port"))),
			)),
		},
		"join that holds a binding of another source": {
			err: doc.Bind(niceyaml.Invalid(errors.Join(
				otherDoc.Bind(yamltest.ErrorAt("bad port", "port")),
				errors.New("plain"),
			), niceyaml.AtPath(paths.Doc().Child("hours")))),
		},
		"bindings of several sources": {
			err: errors.Join(
				at("too early", "hours", "open"),
				otherDoc.Bind(yamltest.ErrorAt("bad port", "port")),
			),
		},
		"documents of one source": {
			err: errors.Join(
				docs[0].Bind(yamltest.ErrorAt("bad a", "a")),
				docs[2].Bind(errors.New("no location")),
				docs[1].Bind(yamltest.ErrorAt("bad b", "b")),
			),
		},
		"wrappers": {
			err: fmt.Errorf("check cafe: %w", fmt.Errorf("%w; %w",
				at("too early", "hours", "open"),
				otherDoc.Bind(yamltest.ErrorAt("bad port", "port")),
			)),
		},
		"wrapper with a sentinel": {
			err: fmt.Errorf("%w: %w", fs.ErrInvalid, at("too early", "hours", "open")),
		},
		"error above a binding": {
			err: doc.Bind(niceyaml.Invalid(at("too early", "hours", "open"),
				niceyaml.WithDetails(yamltest.ErrorAt("see", "hours", "close")),
			)),
		},
		"path that selects nothing": {
			err: errors.Join(
				at("gone", "menu", "99"),
				doc.Bind(niceyaml.NewError("far", niceyaml.AtPosition(position.New(99, 0)))),
			),
		},
		"source with excerpts off": {
			err: errors.Join(secretDoc.Bind(yamltest.ErrorAt("leaked", "token")), at("too early", "hours", "open")),
		},
		"value bound to no document": {
			err: niceyaml.BindValue(niceyaml.NewSummary("2 violations",
				yamltest.ErrorAt("bad port", "port"),
				niceyaml.NewError("bad host", niceyaml.AtPath(paths.Doc().Child("host")),
					niceyaml.WithDetails(otherDoc.Bind(yamltest.ErrorAt("here", "host"))),
				),
			)),
		},
		"many problems": {
			err: doc.Bind(niceyaml.NewSummary("12 violations",
				yamltest.ErrorAt("0", "menu", "0"), yamltest.ErrorAt("1", "menu", "1"),
				yamltest.ErrorAt("2", "menu", "2"), yamltest.ErrorAt("3", "menu", "3"),
				yamltest.ErrorAt("4", "menu", "4"), yamltest.ErrorAt("5", "menu", "5"),
				yamltest.ErrorAt("6", "menu", "6"), yamltest.ErrorAt("7", "menu", "7"),
				yamltest.ErrorAt("8", "menu", "8"), yamltest.ErrorAt("9", "menu", "9"),
				yamltest.ErrorAt("name", "name"), yamltest.ErrorAt("wifi", "wifi"),
			)),
		},
		"options": {
			err: errors.Join(
				longDoc.Bind(niceyaml.NewError("long", niceyaml.AtPosition(position.New(0, 200)))),
				longDoc.Bind(yamltest.ErrorAt("next", "next")),
			),
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(0), niceyaml.WithExcerptWidth(40)},
		},
		"blank lines": {
			err: blank.Bind(niceyaml.NewSummary("2 violations",
				yamltest.ErrorAt("bad b", "b"),
				yamltest.ErrorAt("bad c", "c"),
			)),
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(5)},
		},
		"default width on a long line": {
			err: longDoc.Bind(niceyaml.NewError("long", niceyaml.AtPosition(position.New(0, 250)))),
		},
		"more problems on one line than the default marks": {
			err: manyOnOneLine,
		},
		"marks on one line from the options": {
			err:  manyOnOneLine,
			opts: []niceyaml.ExcerptOption{niceyaml.WithExcerptMarks(2)},
		},
		"every mark of one line": {
			err:  manyOnOneLine,
			opts: []niceyaml.ExcerptOption{niceyaml.WithExcerptMarks(0)},
		},
		"item over several lines past the default marks": {
			err: longItemOfMany,
		},
		"item over several lines past the marks of the options": {
			err:  longItemOfMany,
			opts: []niceyaml.ExcerptOption{niceyaml.WithExcerptMarks(1)},
		},
		"binding over several lines past the default marks": {
			err: longItemOfBindings,
		},
		"binding over several lines past the marks of the options": {
			err:  longItemOfBindings,
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(0), niceyaml.WithExcerptMarks(1)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Error(t, requireDetaches(t, tc.err, tc.opts...))
		})
	}
}

func TestDetach_Nothing(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		for _, err := range []error{nil, (*niceyaml.Error)(nil), (*niceyaml.SourceError)(nil)} {
			assert.NoError(t, niceyaml.Detach(err))
		}
	})

	plain := errors.New("plain")

	tcs := map[string]struct {
		err error
	}{
		"plain error":   {err: plain},
		"unbound error": {err: niceyaml.NewError("unbound", niceyaml.AtPath(paths.Doc().Child("a")))},
		"join":          {err: errors.Join(plain, errors.New("other"))},
		"wrapper":       {err: fmt.Errorf("x: %w", plain)},
		"nil panic in a join": {
			err: errors.Join(plain, (*niceyaml.PanicError)(nil)),
		},
		"nil panic below a wrapper": {
			err: fmt.Errorf("x: %w", (*niceyaml.PanicError)(nil)),
		},
		"nil wrapper that rebuilds itself in a join": {
			err: errors.Join(plain, (*fileError)(nil)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Same(t, tc.err, niceyaml.Detach(tc.err))
		})
	}
}

// keepingError wraps an error with a type that [niceyaml.Detach] does
// not rebuild.
type keepingError struct {
	err error
}

func (w *keepingError) Error() string { return "keeping: " + w.err.Error() }
func (w *keepingError) Unwrap() error { return w.err }

func TestDetach_Detached(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("ab.yaml"))
	doc := yamltest.Document(t, source)

	detached := niceyaml.Detach(doc.Bind(niceyaml.NewSummary("2 violations",
		yamltest.ErrorAt("bad a", "a"),
		yamltest.ErrorAt("bad b", "b"),
	)))

	t.Run("detached error comes back as it is", func(t *testing.T) {
		t.Parallel()

		assert.Same(t, detached, niceyaml.Detach(detached))

		joined := errors.Join(detached, fmt.Errorf("x: %w", detached))
		assert.Same(t, joined, niceyaml.Detach(joined))
	})

	t.Run("binding above a detached one detaches", func(t *testing.T) {
		t.Parallel()

		live := doc.Bind(niceyaml.NewError("again", niceyaml.AtPath(paths.Doc().Child("a")),
			niceyaml.WithDetails(detached)))

		got := requireDetaches(t, live)

		var details []*niceyaml.SourceError

		for _, problem := range yamltest.DetailsOf(got) {
			details = append(details, problem.Bound)
		}

		assert.Contains(t, details, detached)
	})

	t.Run("binding beside a detached one detaches", func(t *testing.T) {
		t.Parallel()

		live := doc.Bind(yamltest.ErrorAt("again", "a"))
		joined := errors.Join(detached, live)

		got := requireDetaches(t, joined)
		bindings := slices.Collect(niceyaml.Bindings(got))
		require.Len(t, bindings, 2)
		assert.Same(t, detached, bindings[0])
	})

	t.Run("error of another type comes back as it is", func(t *testing.T) {
		t.Parallel()

		wrapped := &keepingError{err: doc.Bind(yamltest.ErrorAt("bad a", "a"))}

		got := niceyaml.Detach(wrapped)
		assert.Same(t, wrapped, got)

		bound := yamltest.RequireSourceError(t, got)
		assert.Same(t, source, bound.Source())
	})

	t.Run("error of another type that AsErrors rebuilds stays for errors.As", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.AsErrors(&keepingError{err: errors.Join(
			doc.Bind(niceyaml.NewWarning("old a", niceyaml.AtPath(paths.Doc().Child("a")))),
			doc.Bind(yamltest.ErrorAt("bad b", "b")),
		)})

		wrapper, ok := errors.AsType[*keepingError](err)
		require.True(t, ok)

		// The bindings below the wrapper detach, and errors.As still finds
		// the wrapper, which holds the live ones.
		got := requireDetaches(t, err)

		found, ok := errors.AsType[*keepingError](got)
		require.True(t, ok)
		assert.Same(t, wrapper, found)
	})
}

// fileError wraps an error with a type that rebuilds itself for
// [niceyaml.Detach]. It detaches err before mark, so a mark that holds
// nothing to detach comes last.
type fileError struct {
	err  error
	mark error
	path string
}

func (e *fileError) Error() string { return e.path + ": " + e.err.Error() }
func (e *fileError) Unwrap() error { return e.err }

func (e *fileError) DetachErrors(detach func(error) error) error {
	return &fileError{path: e.path, err: detach(e.err), mark: detach(e.mark)}
}

// WithoutWarnings keeps the wrapper when a check boundary splits off the
// warnings of the error it wraps.
func (e *fileError) WithoutWarnings() error {
	return &fileError{path: e.path, err: niceyaml.WithoutWarnings(e.err), mark: e.mark}
}

func TestDetach_DetachErrors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("ab.yaml"))
	doc := yamltest.Document(t, source)

	tcs := map[string]struct {
		err func() error
	}{
		"wrapper": {
			err: func() error {
				return &fileError{path: "ab.yaml", err: doc.Bind(yamltest.ErrorAt("bad a", "a"))}
			},
		},
		"wrapper that detaches a binding and then a plain error": {
			err: func() error {
				return &fileError{
					path: "ab.yaml",
					err:  doc.Bind(yamltest.ErrorAt("bad a", "a")),
					mark: errors.New("mark"),
				}
			},
		},
		"wrapper below a wrapper of fmt.Errorf": {
			err: func() error {
				return fmt.Errorf(
					"check: %w",
					&fileError{path: "ab.yaml", err: doc.Bind(yamltest.ErrorAt("bad a", "a"))},
				)
			},
		},
		"wrappers of one source in a join": {
			err: func() error {
				return errors.Join(
					&fileError{path: "ab.yaml", err: doc.Bind(yamltest.ErrorAt("bad a", "a"))},
					&fileError{path: "ab.yaml", err: doc.Bind(yamltest.ErrorAt("bad b", "b"))},
				)
			},
		},
		"wrapper that a validation splits": {
			err: func() error {
				v := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
					return &fileError{path: "ab.yaml", err: errors.Join(
						niceyaml.NewWarning("old a", niceyaml.AtPath(paths.Doc().Child("a"))),
						n.Bind(yamltest.ErrorAt("bad b", "b")),
					)}
				})

				return source.ValidateDocuments(t.Context(), v)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.err()
			require.Error(t, err)

			live, ok := errors.AsType[*fileError](err)
			require.True(t, ok)

			got := requireDetaches(t, err)

			found, ok := errors.AsType[*fileError](got)
			require.True(t, ok)
			assert.NotSame(t, live, found)
			assert.Equal(t, "ab.yaml", found.path)

			for bound := range niceyaml.Bindings(found) {
				assert.Nil(t, bound.Source(), bound.Error())
			}
		})
	}

	t.Run("wrapper without a binding comes back as it is", func(t *testing.T) {
		t.Parallel()

		wrapped := &fileError{path: "ab.yaml", err: errors.New("plain")}
		assert.Same(t, wrapped, niceyaml.Detach(wrapped))
	})

	t.Run("wrapper reached twice has one copy", func(t *testing.T) {
		t.Parallel()

		wrapped := &fileError{path: "ab.yaml", err: doc.Bind(yamltest.ErrorAt("bad a", "a"))}
		got := niceyaml.Detach(errors.Join(wrapped, fmt.Errorf("again: %w", wrapped)))

		var copies []*fileError

		joined, ok := got.(interface{ Unwrap() []error }) //nolint:errorlint // The join itself.
		require.True(t, ok)

		for _, branch := range joined.Unwrap() {
			found, ok := errors.AsType[*fileError](branch)
			require.True(t, ok)

			copies = append(copies, found)
		}

		require.Len(t, copies, 2)
		assert.NotSame(t, wrapped, copies[0])
		assert.Same(t, copies[0], copies[1])
	})
}

func TestDetach_PanicError(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("a.yaml"))
	doc := yamltest.Document(t, source)

	t.Run("value that binds an error", func(t *testing.T) {
		t.Parallel()

		live := &niceyaml.PanicError{Value: doc.Bind(yamltest.ErrorAt("bad a", "a")), Stack: []byte("stack")}
		got := requireDetaches(t, fmt.Errorf("check: %w", live))

		found, ok := errors.AsType[*niceyaml.PanicError](got)
		require.True(t, ok)
		assert.NotSame(t, live, found)
		assert.Equal(t, live.Stack, found.Stack)
		assert.Equal(t, live.Error(), found.Error())

		for bound := range niceyaml.Bindings(found) {
			assert.Nil(t, bound.Source(), bound.Error())
		}
	})

	t.Run("value that is not an error", func(t *testing.T) {
		t.Parallel()

		live := &niceyaml.PanicError{Value: "boom"}
		assert.Same(t, live, niceyaml.Detach(live))
	})
}

// detachedFrom returns a weak pointer to a new source of input and the
// detached error that check returns for it. Nothing else holds the
// source.
func detachedFrom(
	ctx context.Context,
	input string,
	check func(context.Context, *niceyaml.Source) error,
) (weak.Pointer[niceyaml.Source], error) {
	source := niceyaml.NewSourceFromString(input, niceyaml.WithName("check.yaml"))

	//nolint:wrapcheck // The test reads the detached error itself.
	return weak.Make(source), niceyaml.Detach(check(ctx, source))
}

func TestDetach_ReleasesSource(t *testing.T) {
	t.Parallel()

	documentOf := func(source *niceyaml.Source) *niceyaml.Node {
		docs := source.Documents()

		return docs[len(docs)-1]
	}

	tcs := map[string]struct {
		check func(context.Context, *niceyaml.Source) error
		input string
	}{
		"path": {
			input: "a: 1\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				return documentOf(source).Bind(yamltest.ErrorAt("bad", "a"))
			},
		},
		"syntax error": {
			input: "a: [1\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				_, err := source.File()

				return err
			},
		},
		"decode error": {
			input: "a: x\n",
			check: func(ctx context.Context, source *niceyaml.Source) error {
				type config struct {
					A []int `yaml:"a"`
				}

				_, err := documentOf(source).Decode[config](ctx)

				return err
			},
		},
		"path with no single document": {
			input: "a: 1\n---\nb: 2\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				return source.Bind(yamltest.ErrorAt("bad", "a"))
			},
		},
		"documents": {
			input: "a: 1\n---\nb: 2\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				docs := source.Documents()

				return errors.Join(
					docs[0].Bind(yamltest.ErrorAt("bad a", "a")),
					docs[1].Bind(errors.New("no location")),
				)
			},
		},
		"wrapped": {
			input: "a: 1\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				return fmt.Errorf(
					"check: %w",
					documentOf(source).Bind(niceyaml.NewWarning("old", niceyaml.AtPath(paths.Doc().Child("a")))),
				)
			},
		},
		"wrapper that rebuilds itself": {
			input: "a: 1\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				return &fileError{path: "check.yaml", err: documentOf(source).Bind(yamltest.ErrorAt("bad", "a"))}
			},
		},
		"panic with a binding": {
			input: "a: 1\n",
			check: func(_ context.Context, source *niceyaml.Source) error {
				return &niceyaml.PanicError{Value: documentOf(source).Bind(yamltest.ErrorAt("bad", "a"))}
			},
		},
		"validator that wraps a warning and an error": {
			input: "a: 1\nb: 2\n",
			check: func(ctx context.Context, source *niceyaml.Source) error {
				return source.ValidateDocuments(ctx, wrappingValidator)
			},
		},
		"report of the warnings of a validation": {
			input: "a: 1\nb: 2\n",
			check: func(ctx context.Context, source *niceyaml.Source) error {
				warnings := niceyaml.NewWarnings()

				return warnings.Report(source.ValidateDocuments(warnings.Context(ctx), wrappingValidator))
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source, detached := detachedFrom(t.Context(), tc.input, tc.check)
			require.Error(t, detached)

			runtime.GC()
			runtime.GC()

			assert.Nil(t, source.Value())
			assert.NotEmpty(t, niceyaml.FormatError(detached))
		})
	}
}

// TestDetach_ReleasesParse checks that a detached decode error does not keep
// the parse of the document alive. The tokens of a parse link to each
// other, so any node that the error keeps also keeps the first token.
func TestDetach_ReleasesParse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		input  string
	}{
		"check below a map": {
			input: "ports:\n  0x10:\n    name: \"\"\n",
			decode: func(ctx context.Context, doc *niceyaml.Node) error {
				_, err := doc.Decode[map[string]map[int]dataCheckedPort](ctx)

				return err
			},
		},
		"check that names a map entry": {
			input: "cfg:\n  ports:\n    0x10:\n      name: \"\"\n",
			decode: func(ctx context.Context, doc *niceyaml.Node) error {
				_, err := doc.Decode[map[string]dataPorts](ctx)

				return err
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first, detached := func() (weak.Pointer[token.Token], error) {
				source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("check.yaml"))
				doc := source.Documents()[0]

				//nolint:wrapcheck // The test reads the detached error itself.
				return weak.Make(doc.AST().GetToken()), niceyaml.Detach(tc.decode(t.Context(), doc))
			}()
			require.Error(t, detached)

			runtime.GC()
			runtime.GC()

			assert.Nil(t, first.Value())
			assert.NotEmpty(t, niceyaml.FormatError(detached))
		})
	}
}

func TestDetach_Bindings(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n---\nc:\n  d: 3\n",
		niceyaml.WithName("many.yaml"), niceyaml.WithFilePath("/srv/many.yaml"))
	docs := yamltest.AllDocuments(t, source, 3)
	unparsed := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n", niceyaml.WithName("pair.yaml"))

	tcs := map[string]struct {
		err          error
		wantIndex    int
		wantDocument bool
	}{
		"binding at a path": {
			err:          docs[2].Bind(yamltest.ErrorAt("bad d", "c", "e")),
			wantIndex:    2,
			wantDocument: true,
		},
		"binding with no location": {
			err:          docs[1].Bind(errors.New("plain")),
			wantIndex:    1,
			wantDocument: true,
		},
		"binding in no document": {
			err: unparsed.Bind(yamltest.ErrorAt("bad", "a")),
		},
		"value that came from no document": {
			err: niceyaml.BindValue(yamltest.ErrorAt("bad", "a")),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			live := yamltest.RequireSourceError(t, tc.err)
			bound := yamltest.RequireSourceError(t, requireDetaches(t, tc.err))

			assert.Nil(t, bound.Source())
			assert.Equal(t, live.Name(), bound.Name())
			assert.Equal(t, live.FilePath(), bound.FilePath())

			_, ok := bound.Node()
			assert.False(t, ok)

			_, ok = bound.Document()
			assert.False(t, ok)

			index, ok := bound.DocumentIndex()
			assert.Equal(t, tc.wantDocument, ok)
			assert.Equal(t, tc.wantIndex, index)

			assert.Equal(t, live.Message(), bound.Message())

			livePath, liveOK := live.Path()
			path, ok := bound.Path()
			assert.Equal(t, liveOK, ok)
			assert.True(t, livePath.Equal(path))

			nearest, nearOK := live.Nearest()
			got, ok := bound.Nearest()
			assert.Equal(t, nearOK, ok)
			assert.True(t, nearest.Equal(got))

			livePos, liveOK := live.Position()
			pos, ok := bound.Position()
			assert.Equal(t, liveOK, ok)
			assert.Equal(t, livePos, pos)

			reason := live.Unresolved()
			if reason != nil {
				require.EqualError(t, bound.Unresolved(), reason.Error())
			} else {
				assert.NoError(t, bound.Unresolved())
			}
		})
	}
}

func TestDetach_Is(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: [1\n", niceyaml.WithName("bad.yaml"))
	_, syntaxErr := source.File()
	require.Error(t, syntaxErr)

	decoded := yamltest.FirstDocument(t, "n: x\n")

	type config struct {
		N int `yaml:"n"`
	}

	_, decodeErr := decoded.Decode[config](t.Context())
	require.Error(t, decodeErr)

	custom := &customTestError{msg: "custom"}
	customErr := yamltest.FirstDocument(t, "a: 1\n").Bind(niceyaml.Locate(custom,
		niceyaml.AtPath(paths.Doc().Child("a"))))

	tcs := map[string]struct {
		err   error
		is    []error
		isNot []error
		// An error that errors.As finds in the detached copy, of the type
		// of the target.
		as error
	}{
		"syntax error": {
			err:   syntaxErr,
			is:    []error{niceyaml.ErrSyntax},
			isNot: []error{niceyaml.ErrDecode},
		},
		"decode error": {
			err: fmt.Errorf("load: %w", decodeErr),
			is:  []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"custom error": {
			err: fmt.Errorf("%w: %w", fs.ErrInvalid, customErr),
			is:  []error{fs.ErrInvalid, custom},
			as:  custom,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			detached := requireDetaches(t, tc.err)

			for _, target := range tc.is {
				require.ErrorIs(t, detached, target)
			}

			for _, target := range tc.isNot {
				require.NotErrorIs(t, detached, target)
			}

			if tc.as != nil {
				found, ok := errors.AsType[*customTestError](detached)
				require.True(t, ok)
				assert.Same(t, tc.as, found)
			}

			bound, ok := errors.AsType[*niceyaml.SourceError](detached)
			require.True(t, ok)
			assert.Nil(t, bound.Source())
		})
	}
}

func TestDetach_Excerpt(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1", "b: 2", "c: 3", "d: 4", "e: 5", "f: 6", "g: 7",
	), niceyaml.WithName("letters.yaml"))
	doc := yamltest.Document(t, source)

	err := doc.Bind(niceyaml.NewError("bad g", niceyaml.AtPath(paths.Doc().Child("g")),
		niceyaml.WithDetails(yamltest.ErrorAt("see a", "a"))))
	detached := yamltest.RequireSourceError(t, niceyaml.Detach(err, niceyaml.WithContextLines(1)))

	want := stringtest.JoinLF(
		"   1 | a: 1",
		"     |    ^ see a",
		"   2 | b: 2",
		"     | ...",
		"   6 | f: 6",
		"   7 | g: 7",
		"     |    ^",
	)

	live, ok := yamltest.RequireSourceError(t, err).Excerpt(niceyaml.WithContextLines(1))
	require.True(t, ok)
	assert.Equal(t, want, live.String())

	// The options of Detach hold, whatever options the excerpt gets.
	for _, opts := range [][]niceyaml.ExcerptOption{nil, {niceyaml.WithContextLines(3)}} {
		view, ok := detached.Excerpt(opts...)
		require.True(t, ok)
		assert.Equal(t, want, view.String())
		assert.Equal(t, []int{1, 2, 6, 7}, yamltest.LineNumbers(view))
	}

	// The lines of the excerpt belong to no view of the source.
	view := source.View()
	assert.False(t, niceyaml.Annotate(detached, view))
	assert.False(t, detached.Annotate(view))

	rep := niceyaml.NewErrorReport(detached)
	require.Len(t, rep.Excerpts, 1)
	assert.Nil(t, rep.Excerpts[0].Source)
	assert.Equal(t, "letters.yaml", rep.Excerpts[0].Name)
}

func TestDetach_ExcerptsOff(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(
		"token: abc\n",
		niceyaml.WithName("secret.yaml"),
		niceyaml.WithExcerpts(false),
	)

	detached := yamltest.RequireSourceError(
		t,
		requireDetaches(t, yamltest.Bind(t, source, yamltest.ErrorAt("leaked", "token"))),
	)

	_, ok := detached.Excerpt()
	assert.False(t, ok)
	assert.Equal(t, "secret.yaml:1:8: $.token: leaked", niceyaml.FormatError(detached))
}

func TestDetach_Place(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "request:\n  port: 0\n", niceyaml.WithName("app.yaml"))
	value := niceyaml.BindValue(yamltest.ErrorAt("too low", "port"))
	at := paths.Doc().Child("request")

	want := doc.Bind(niceyaml.Rebase(value, at))
	got := doc.Bind(niceyaml.Rebase(requireDetaches(t, value), at))

	assert.Equal(t, niceyaml.FormatError(want), niceyaml.FormatError(got))
	assert.Equal(t, "app.yaml:2:9: $.request.port: too low", got.Error())
}

func TestDetach_Rebind(t *testing.T) {
	t.Parallel()

	first := yamltest.FirstDocument(t, "a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := yamltest.FirstDocument(t, "c: 3\n", niceyaml.WithName("second.yaml"))

	err := first.Bind(yamltest.ErrorAt("bad a", "a"))
	detached := requireDetaches(t, err)

	rebind := func(err error) error {
		return second.Bind(niceyaml.Invalid(err, niceyaml.WithDetails(yamltest.ErrorAt("see c", "c"))))
	}

	want, got := rebind(err), rebind(detached)
	assert.Equal(t, niceyaml.FormatError(want), niceyaml.FormatError(got))
	assert.Equal(t, detachedRows(want), detachedRows(got))
}

func TestDetach_AsErrors(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "a: 1\nb: 2\n", niceyaml.WithName("ab.yaml"))
	err := doc.Bind(errors.Join(
		niceyaml.NewWarning("old a", niceyaml.AtPath(paths.Doc().Child("a"))),
		yamltest.ErrorAt("bad b", "b"),
	))

	detached := requireDetaches(t, err)

	want := niceyaml.AsErrors(err)
	got := niceyaml.AsErrors(detached)

	assert.Equal(t, want.Error(), got.Error())
	assert.Equal(t, niceyaml.FormatError(want), niceyaml.FormatError(got))
	assert.Equal(t, detachedRows(want), detachedRows(got))
}

// TestDetach_RetainedSize checks that the detached errors of short files
// hold less memory than the errors hold with their sources. The test reads
// the heap of the whole process, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestDetach_RetainedSize(t *testing.T) {
	tcs := map[string]struct {
		lines    int
		problems int
	}{
		"one problem in a short file":      {lines: 11, problems: 1},
		"several problems in a short file": {lines: 43, problems: 3},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			var input strings.Builder

			for i := range tc.lines {
				fmt.Fprintf(&input, "key%d: value\n", i)
			}

			// The errors of 100 files, each with its problems spread evenly
			// through the file.
			check := func(keep func(error) error) func() any {
				return func() any {
					kept := make([]error, 0, 100)

					for range cap(kept) {
						source := niceyaml.NewSourceFromString(input.String(), niceyaml.WithName("check.yaml"))
						step := tc.lines / tc.problems

						problems := make([]error, 0, tc.problems)
						for i := range tc.problems {
							problems = append(problems, yamltest.ErrorAt("bad", fmt.Sprintf("key%d", i*step+step/2)))
						}

						kept = append(kept, keep(source.Bind(errors.Join(problems...))))
					}

					return kept
				}
			}

			bound := retainedBytes(check(func(err error) error { return err }))
			detached := retainedBytes(check(func(err error) error { return niceyaml.Detach(err) }))

			require.Positive(t, detached)
			assert.Less(t, detached, bound)
		})
	}
}

// TestDetach_RetainedScalarLines checks that the copy of a line of a scalar
// that spans many lines does not keep the text of the whole scalar. Each
// excerpt shows lines of the scalar other than the line that carries its
// value. The test reads the heap of the whole process, so it does not run
// in parallel.
//
//nolint:paralleltest // See above.
func TestDetach_RetainedScalarLines(t *testing.T) {
	text := strings.Repeat("  0123456789 0123456789 0123456\n", 3000)

	tcs := map[string]string{
		"first line of a literal scalar": stringtest.JoinLF("a: 1", "key: x", "data: |", text),
		"first line of a folded scalar":  stringtest.JoinLF("a: 1", "key: x", "data: >", text),
		"last lines of a plain scalar":   stringtest.JoinLF("data: a", text+"key: x", ""),
		"last lines of a quoted scalar":  stringtest.JoinLF(`data: "a`, text+`  b"`, "key: x", ""),
	}

	for name, input := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			var shown string

			detached := retainedBytes(func() any {
				kept := make([]error, 0, 5)

				for range cap(kept) {
					source := niceyaml.NewSourceFromString(input)
					kept = append(kept, niceyaml.Detach(source.Bind(yamltest.ErrorAt("bad", "key"))))
				}

				shown = niceyaml.FormatError(kept[0])

				return kept
			})

			require.Contains(t, shown, "0123456789")
			require.Positive(t, detached)

			// Each error holds a few kilobytes. An error that keeps the
			// text of the scalar holds more than the file.
			assert.Less(t, detached, float64(len(input)))

			runtime.KeepAlive(input)
		})
	}
}

// retainedBytes returns the number of bytes of heap that the value build
// returns keeps alive. It reads the heap of the whole process, so a test
// that calls it does not run in parallel.
func retainedBytes(build func() any) float64 {
	var before, after runtime.MemStats

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&before)

	kept := build()

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(kept)

	return float64(after.HeapAlloc) - float64(before.HeapAlloc)
}
