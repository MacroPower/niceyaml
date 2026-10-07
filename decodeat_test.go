package niceyaml_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// openHours is the value the tests of this file read at a path. It has
// no Validate method, so each test names the checks that run.
type openHours struct {
	Open  int `yaml:"open"`
	Close int `yaml:"close"`
}

// requiresClose returns a [niceyaml.Validator] that reads the key close
// of the node it gets, as a check for a required key does, and returns
// the error of that read.
func requiresClose() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		_, err := n.At(paths.Current().Child("close"))

		return err //nolint:wrapcheck // The validator returns the error of the read as it is.
	})
}

// pathRead reads the value at a path as one Go type, in the two ways
// the tests compare.
type pathRead struct {
	// Reads through [niceyaml.Node.DecodeAt].
	decodeAt func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error)
	// Reads through [niceyaml.Node.At] and then [niceyaml.Node.Decode],
	// the two calls DecodeAt stands for.
	scoped func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error)
}

// readAs returns a [pathRead] that decodes into a T with opts.
func readAs[T any](opts ...niceyaml.DecodeOption) pathRead {
	return pathRead{
		decodeAt: func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error) {
			return n.DecodeAt[T](ctx, path, opts...)
		},
		scoped: func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error) {
			node, err := n.At(path)
			if err != nil {
				var zero T

				return zero, err //nolint:wrapcheck // The test compares the error of the call.
			}

			return node.Decode[T](ctx, opts...)
		},
	}
}

func TestNode_DecodeAt(t *testing.T) {
	t.Parallel()

	const input = `
		kind: Deployment
		replicas: 3
		note: ~
		spec:
		  hours:
		    open: 9
		    close: 17
		    days: 5
		items:
		  - a
		  - b
		ref: *nope
	`

	document := func(t *testing.T) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocumentWithPath(t, stringtest.Input(input), "cfg.yaml")
	}

	specPath := paths.Doc().Child("spec")
	hoursPath := specPath.Child("hours")

	tcs := map[string]struct {
		want any
		// The sentinel the error matches, or nil for a read that succeeds.
		err error
		// The two reads, which decode into one type.
		read pathRead
		// The message of the error.
		wantErr string
		// The path of the Node that reads, or none for the root.
		scope paths.Path
		path  paths.Path
	}{
		"a string": {
			path: paths.Current().Child("kind"),
			read: readAs[string](),
			want: "Deployment",
		},
		"an integer": {
			path: paths.Current().Child("replicas"),
			read: readAs[int](),
			want: 3,
		},
		"a sequence": {
			path: paths.Current().Child("items"),
			read: readAs[[]string](),
			want: []string{"a", "b"},
		},
		"a struct": {
			path: hoursPath,
			read: readAs[openHours](),
			want: openHours{Open: 9, Close: 17},
		},
		"a null reads as the zero value": {
			path: paths.Current().Child("note"),
			read: readAs[string](),
			want: "",
		},
		"an @ path reads from the receiver": {
			scope: specPath,
			path:  paths.Current().Child("hours", "open"),
			read:  readAs[int](),
			want:  9,
		},
		"a $ path reads from the root of the document": {
			scope: specPath,
			path:  paths.Doc().Child("kind"),
			read:  readAs[string](),
			want:  "Deployment",
		},
		"a key the root leaves out": {
			path:    paths.Current().Child("name"),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrNotFound,
			wantErr: "cfg.yaml:1:1: $.name: not found",
		},
		"a key the receiver leaves out": {
			scope:   hoursPath,
			path:    paths.Current().Child("lunch"),
			read:    readAs[int](),
			want:    0,
			err:     paths.ErrNotFound,
			wantErr: "cfg.yaml:5:3: $.spec.hours.lunch: not found",
		},
		"a key looked up in a scalar": {
			path:    paths.Current().Child("kind", "group"),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrNotFound,
			wantErr: "cfg.yaml: resolve $.kind.group: not found",
		},
		"a value of the wrong type": {
			path:    paths.Current().Child("kind"),
			read:    readAs[int](),
			want:    0,
			err:     niceyaml.ErrDecode,
			wantErr: "cfg.yaml:1:7: $.kind: expected integer, got string",
		},
		"a field the type lacks under WithDisallowUnknownFields": {
			path:    hoursPath,
			read:    readAs[openHours](niceyaml.WithDisallowUnknownFields(true)),
			want:    openHours{},
			err:     niceyaml.ErrDecode,
			wantErr: `cfg.yaml:8:5: $.spec.hours.days~: unknown field "days"`,
		},
		"a wildcard path": {
			path:    paths.Current().Child("items").IndexAll(),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrWildcard,
			wantErr: "cfg.yaml: resolve $.items[*]: wildcard path matches any number of nodes",
		},
		"an alias that does not resolve": {
			path:    paths.Current().Child("ref"),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrAlias,
			wantErr: "cfg.yaml: resolve $.ref: alias does not resolve: *nope has no anchor before it",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := document(t)
			if tc.scope.Len() > 0 {
				node = yamltest.At(t, node, tc.scope)
			}

			got, err := tc.read.decodeAt(t.Context(), node, tc.path)
			assert.Equal(t, tc.want, got)

			// DecodeAt returns what At and then Decode return.
			scoped, scopedErr := tc.read.scoped(t.Context(), node, tc.path)
			assert.Equal(t, scoped, got)

			if tc.err == nil {
				require.NoError(t, err)
				require.NoError(t, scopedErr)

				return
			}

			require.ErrorIs(t, err, tc.err)
			require.EqualError(t, err, tc.wantErr)
			require.EqualError(t, scopedErr, tc.wantErr)
		})
	}

	t.Run("a validator checks the node the path selects", func(t *testing.T) {
		t.Parallel()

		var seen string

		late := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = n.Path().String()

			return niceyaml.NewError("opens too late", niceyaml.AtPath(paths.Current().Child("open")))
		})

		got, err := document(t).DecodeAt[openHours](t.Context(), hoursPath, niceyaml.WithValidator(late))
		require.EqualError(t, err, "cfg.yaml:6:11: $.spec.hours.open: opens too late")
		assert.Equal(t, "$.spec.hours", seen)
		assert.Equal(t, openHours{}, got)

		// The error is bound through the Node the validator got.
		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "$.spec.hours", bound.Node().Path().String())
	})

	t.Run("a validator that reads a required key reports not found for a present node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, "spec:\n  hours:\n    open: 9\n", "cfg.yaml")

		_, err := doc.At(hoursPath)
		require.NoError(t, err)

		// The error matches paths.ErrNotFound, and the node at the path
		// is present, so the sentinel does not show an absent value.
		_, err = doc.DecodeAt[openHours](t.Context(), hoursPath, niceyaml.WithValidator(requiresClose()))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.EqualError(t, err, "cfg.yaml:2:3: $.spec.hours.close: not found")
	})

	t.Run("Rebase binds a check of the value where its Node binds it", func(t *testing.T) {
		t.Parallel()

		doc := document(t)

		// A check written for the type, which reads `@` paths from the
		// value.
		check := func(h openHours) error {
			if h.Open < 12 {
				return niceyaml.NewError("opens before noon", niceyaml.AtPath(paths.Current().Child("open")))
			}

			return nil
		}

		h, err := doc.DecodeAt[openHours](t.Context(), hoursPath)
		require.NoError(t, err)

		const want = "cfg.yaml:6:11: $.spec.hours.open: opens before noon"

		require.EqualError(t, doc.Bind(niceyaml.Rebase(check(h), hoursPath)), want)
		require.EqualError(t, yamltest.At(t, doc, hoursPath).Bind(check(h)), want)
	})

	t.Run("a document with no content returns the error of At", func(t *testing.T) {
		t.Parallel()

		versionPath := paths.Current().Child("version")

		tcs := map[string]struct {
			input string
			// The message of the error, or none for a path that selects
			// a node.
			wantErr string
			path    paths.Path
		}{
			"a key of an empty file": {
				input:   "",
				path:    versionPath,
				wantErr: "cfg.yaml: resolve $.version: not found: document has no content",
			},
			"a key of a file of comments": {
				input:   "# nothing\n",
				path:    versionPath,
				wantErr: "cfg.yaml: resolve $.version: not found: document has no content",
			},
			"a key below a header": {
				input:   "---\n",
				path:    versionPath,
				wantErr: "cfg.yaml: resolve $.version: not found: document has no content",
			},
			"the root of an empty file": {
				input:   "",
				path:    paths.Current(),
				wantErr: "cfg.yaml: resolve $: not found: document has no content",
			},
			"the root below a header is a null": {
				input: "---\n",
				path:  paths.Current(),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, tc.input, "cfg.yaml")
				require.True(t, doc.IsEmpty())

				// A decode of the root returns the zero value.
				whole, err := doc.Decode[int](t.Context())
				require.NoError(t, err)
				assert.Zero(t, whole)

				got, err := doc.DecodeAt[int](t.Context(), tc.path)
				assert.Zero(t, got)

				_, atErr := doc.At(tc.path)

				if tc.wantErr == "" {
					require.NoError(t, err)
					require.NoError(t, atErr)

					return
				}

				require.ErrorIs(t, err, paths.ErrNoDocument)
				require.ErrorIs(t, err, paths.ErrNotFound)
				require.EqualError(t, err, tc.wantErr)
				require.EqualError(t, atErr, tc.wantErr)
			})
		}
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("kind: [\n", niceyaml.WithFilePath("cfg.yaml")).AllDocuments()
		require.Len(t, docs, 1)
		require.Error(t, docs[0].Err())

		got, err := docs[0].DecodeAt[string](t.Context(), paths.Current().Child("kind"))
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.ErrorIs(t, err, docs[0].Err())
		assert.Empty(t, got)
	})
}
