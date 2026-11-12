package schema_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
)

// document returns a document for resolvers that never read it.
func document(t *testing.T) *niceyaml.Node {
	t.Helper()

	return yamltest.FirstDocument(t, "key: value\n")
}

// loadFor resolves r for doc and loads the schema it names. It returns
// the ref's Key alongside the loaded bytes. Resolve itself must succeed,
// and loadFor returns only the Load error.
func loadFor(t *testing.T, r schema.Resolver, doc *niceyaml.Node) (string, []byte, error) {
	t.Helper()

	ref, err := r.Resolve(t.Context(), doc)
	require.NoError(t, err)

	data, err := schema.NewRegistry(schema.WithAccess(schema.Unrestricted())).Load(t.Context(), ref)

	return ref.Key(), data, err //nolint:wrapcheck // Tests inspect the loader's own error.
}

// load is [loadFor] with a document that r never reads.
func load(t *testing.T, r schema.Resolver) (string, []byte, error) {
	t.Helper()

	return loadFor(t, r, document(t))
}

// failing returns a resolver that names no schema and reports err for
// every document.
func failing(err error) schema.Resolver {
	return schema.ResolverFunc(func(context.Context, *niceyaml.Node) (schema.Ref, error) {
		return schema.Ref{}, err
	})
}

// Compile-time interface satisfaction checks.
var (
	_ schema.Resolver = schema.ResolverFunc(nil)
	_ schema.Resolver = schema.Ref{}
)

func TestRef_Resolve(t *testing.T) {
	t.Parallel()

	t.Run("names itself for every document", func(t *testing.T) {
		t.Parallel()

		ref := loading("config.json", []byte(`{"type": "object"}`), nil)

		got, err := ref.Resolve(t.Context(), document(t))
		require.NoError(t, err)
		assert.Equal(t, ref.Key(), got.Key())

		data, err := schema.NewRegistry().Load(t.Context(), got)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
	})

	t.Run("zero ref names no schema", func(t *testing.T) {
		t.Parallel()

		got, err := schema.Ref{}.Resolve(t.Context(), document(t))
		require.NoError(t, err)
		assert.Empty(t, got.Key())
	})
}

func TestRef_Name(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		ref  schema.Ref
		want string
	}{
		"URL with a password": {
			ref:  schema.URL("https://user:secret@example.com/s.json#/$defs/a"),
			want: "https://user:xxxxx@example.com/s.json#/$defs/a",
		},
		"URL with no password": {
			ref:  schema.URL("https://example.com/s.json"),
			want: "https://example.com/s.json",
		},
		"file in a file system": {
			ref:  schema.FileFS(yamltest.MapFS(nil), "schemas/s.json"),
			want: "schemas/s.json",
		},
		"loadable": {
			ref:  loading("config.json", nil, nil),
			want: "config.json",
		},
		"zero Ref": {
			ref:  schema.Ref{},
			want: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.ref.Name())
		})
	}
}

func TestRef_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		ref  schema.Ref
		want string
	}{
		"URL with a password": {
			ref:  schema.URL("https://user:secret@example.com/s.json#/$defs/a"),
			want: "https://user:xxxxx@example.com/s.json#/$defs/a",
		},
		"URL with no password": {
			ref:  schema.URL("https://example.com/s.json"),
			want: "https://example.com/s.json",
		},
		"file in a file system": {
			ref:  schema.FileFS(yamltest.MapFS(nil), "schemas/s.json"),
			want: "schemas/s.json",
		},
		"loadable": {
			ref:  loading("config.json", nil, nil),
			want: "config.json",
		},
		"zero Ref": {
			ref:  schema.Ref{},
			want: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.ref.String())
			assert.Equal(t, tc.ref.Name(), tc.ref.String())
			assert.Equal(t, "using "+tc.want, fmt.Sprintf("using %v", tc.ref))
			assert.Equal(t, "using "+tc.want, fmt.Sprintf("using %s", tc.ref))
			assert.Equal(t, "using "+tc.want, fmt.Sprintf("using %v", &tc.ref))
		})
	}
}

func TestRef_LogValue(t *testing.T) {
	t.Parallel()

	// Without the time of a record, the output is the same on every
	// run.
	dropTime := func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}

		return a
	}

	tcs := map[string]struct {
		ref      schema.Ref
		want     string
		wantText string
		wantJSON string
	}{
		"URL with a password": {
			ref:      schema.URL("https://user:secret@example.com/s.json"),
			want:     "https://user:xxxxx@example.com/s.json",
			wantText: "level=INFO msg=resolved schema=https://user:xxxxx@example.com/s.json\n",
			wantJSON: `{"level":"INFO","msg":"resolved","schema":"https://user:xxxxx@example.com/s.json"}`,
		},
		"file in a file system": {
			ref:      schema.FileFS(yamltest.MapFS(nil), "schemas/s.json"),
			want:     "schemas/s.json",
			wantText: "level=INFO msg=resolved schema=schemas/s.json\n",
			wantJSON: `{"level":"INFO","msg":"resolved","schema":"schemas/s.json"}`,
		},
		"loadable": {
			ref:      loading("config.json", nil, nil),
			want:     "config.json",
			wantText: "level=INFO msg=resolved schema=config.json\n",
			wantJSON: `{"level":"INFO","msg":"resolved","schema":"config.json"}`,
		},
		"zero Ref": {
			ref:      schema.Ref{},
			want:     "",
			wantText: "level=INFO msg=resolved schema=\"\"\n",
			wantJSON: `{"level":"INFO","msg":"resolved","schema":""}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The assertions read the kind and the string, because
			// assert.Equal on two slog.Value structs compares only the
			// length and the first byte of the strings.
			got := tc.ref.LogValue()
			assert.Equal(t, slog.KindString, got.Kind())
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.ref.Name(), got.String())

			opts := &slog.HandlerOptions{ReplaceAttr: dropTime}

			var text bytes.Buffer

			slog.New(slog.NewTextHandler(&text, opts)).Info("resolved", slog.Any("schema", tc.ref))
			assert.Equal(t, tc.wantText, text.String())

			var js bytes.Buffer

			slog.New(slog.NewJSONHandler(&js, opts)).Info("resolved", slog.Any("schema", tc.ref))
			assert.JSONEq(t, tc.wantJSON, js.String())
		})
	}
}

func TestRef_WithFragment(t *testing.T) {
	t.Parallel()

	const defs = `{
		"definitions": {
			"Foo": {"type": "object", "required": ["foo"]},
			"Bar": {"type": "object", "required": ["bar"]}
		}
	}`

	bundle := yamltest.MapFS(map[string]string{"schemas/defs.json": defs})

	dir := t.TempDir()
	onDisk := yamltest.WriteFile(t, dir, "defs.json", defs)

	t.Run("keys", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			ref      schema.Ref
			fragment string
			want     string
			wantName string
		}{
			"file on disk": {
				ref:      schema.File(onDisk),
				fragment: "/definitions/Foo",
				want:     schema.File(onDisk).Key() + "#/definitions/Foo",
			},
			"file in a file system": {
				ref:      schema.FileFS(bundle, "schemas/defs.json"),
				fragment: "/definitions/Foo",
				want:     "file:///schemas/defs.json#/definitions/Foo",
				wantName: "schemas/defs.json#/definitions/Foo",
			},
			"URL": {
				ref:      schema.URL("https://example.com/defs.json"),
				fragment: "/$defs/a",
				want:     "https://example.com/defs.json#/$defs/a",
			},
			"URL with a fragment": {
				ref:      schema.URL("https://example.com/defs.json#/$defs/a"),
				fragment: "/$defs/b",
				want:     "https://example.com/defs.json#/$defs/b",
			},
			"file URL with a fragment": {
				ref:      fileOrURL(t, "", plainFileURL(onDisk)+"#/definitions/Foo"),
				fragment: "/definitions/Bar",
				want:     schema.File(onDisk).Key() + "#/definitions/Bar",
			},
			"empty fragment names the whole document": {
				ref:  schema.URL("https://example.com/defs.json#/$defs/a"),
				want: "https://example.com/defs.json",
			},
			"empty fragment on a file in a file system": {
				ref:      schema.FileFS(bundle, "schemas/defs.json").WithFragment("/definitions/Foo"),
				want:     "file:///schemas/defs.json",
				wantName: "schemas/defs.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := tc.ref.WithFragment(tc.fragment)
				assert.Equal(t, tc.want, got.Key())

				if tc.wantName != "" {
					assert.Equal(t, tc.wantName, got.Name())
				}
			})
		}
	})

	t.Run("a bundle serves each definition", func(t *testing.T) {
		t.Parallel()

		kindPath := paths.Doc().Child("kind")
		byKind := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			var kind string

			_, err := doc.DecodeIfPresent(ctx, kindPath, &kind)
			if err != nil {
				return schema.Ref{}, fmt.Errorf("read kind: %w", err)
			}

			return schema.FileFS(bundle, "schemas/defs.json").WithFragment("/definitions/" + kind), nil
		})

		reg := schema.NewRegistry(schema.WithResolvers(byKind))

		require.NoError(t, reg.Check(t.Context(), yamltest.FirstDocument(t, "kind: Foo\nfoo: 1\n")))
		require.NoError(t, reg.Check(t.Context(), yamltest.FirstDocument(t, "kind: Bar\nbar: 1\n")))

		err := reg.Check(t.Context(), yamltest.FirstDocument(t, "kind: Foo\nbar: 1\n"))
		require.ErrorContains(t, err, `missing required property "foo"`)
	})

	t.Run("a file on disk serves a definition", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.File(onDisk).WithFragment("/definitions/Bar")))

		require.NoError(t, reg.Check(t.Context(), yamltest.FirstDocument(t, "bar: 1\n")))

		err := reg.Check(t.Context(), yamltest.FirstDocument(t, "foo: 1\n"))
		require.ErrorContains(t, err, `missing required property "bar"`)
	})

	t.Run("an escaped name selects its definition", func(t *testing.T) {
		t.Parallel()

		const odd = `{
			"definitions": {
				"a/b": {"type": "object", "required": ["slash"]},
				"a~b": {"type": "object", "required": ["tilde"]},
				"50%": {"type": "object", "required": ["percent"]},
				"a b": {"type": "object", "required": ["space"]}
			}
		}`

		oddFS := yamltest.MapFS(map[string]string{"odd.json": odd})
		escape := strings.NewReplacer("~", "~0", "/", "~1")

		tcs := map[string]struct {
			name string
			want string
		}{
			"slash":   {name: "a/b", want: "slash"},
			"tilde":   {name: "a~b", want: "tilde"},
			"percent": {name: "50%", want: "percent"},
			"space":   {name: "a b", want: "space"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				fragment := "/definitions/" + url.PathEscape(escape.Replace(tc.name))
				reg := schema.NewRegistry(schema.WithResolvers(schema.FileFS(oddFS, "odd.json").WithFragment(fragment)))

				require.NoError(t, reg.Check(t.Context(), yamltest.FirstDocument(t, tc.want+": 1\n")))

				err := reg.Check(t.Context(), yamltest.FirstDocument(t, "other: 1\n"))
				require.ErrorContains(t, err, `missing required property "`+tc.want+`"`)
			})
		}
	})

	t.Run("a Ref that a document names keeps the mark", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, "a: 1\n", filepath.Join(dir, "app.yaml"))

		ref, err := schema.RefBeside(doc, "./defs.json")
		require.NoError(t, err)

		_, err = schema.NewRegistry().Schema(t.Context(), ref.WithFragment("/definitions/Foo"))
		require.ErrorIs(t, err, schema.ErrAccessNotSet)
	})

	t.Run("a Ref without a location panics", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			ref schema.Ref
		}{
			"zero Ref":   {ref: schema.Ref{}},
			"loadable":   {ref: loading("config.json", nil, nil)},
			"embedded":   {ref: schema.Embedded([]byte(defs))},
			"schema Ref": {ref: schema.MustCompile([]byte(defs)).Ref()},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, "schema.Ref.WithFragment: the Ref does not name a file or a URL", func() {
					tc.ref.WithFragment("/definitions/Foo")
				})
			})
		}
	})
}

func TestRef_NamedAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	app := filepath.Join(dir, "app.yaml")
	schemaPath := paths.Doc().Child("$schema")

	// A resolver that reads the reference from a field and marks the Ref
	// there.
	field := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
		var s string

		found, err := doc.DecodeIfPresent(ctx, schemaPath, &s)
		if err != nil {
			return schema.Ref{}, fmt.Errorf("read $schema: %w", err)
		}

		if !found {
			return schema.Ref{}, schema.ErrNoMatch
		}

		at := niceyaml.AtPath(schemaPath)

		ref, err := schema.RefBeside(doc, s)
		if err != nil {
			return schema.Ref{}, niceyaml.Locate(err, at)
		}

		return ref.NamedAt(doc, at), nil
	})

	reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(field))

	t.Run("binds the error of the schema at the field", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n$schema: ./missing.json\n",
			niceyaml.WithName("app.yaml"), niceyaml.WithFilePath(app))

		_, err := reg.Lookup(t.Context(), source.Documents()[0])
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.True(t, strings.HasPrefix(err.Error(), "app.yaml:2:10: "), "got %q", err.Error())
		assert.False(t, niceyaml.IsInvalid(err))

		at, ok := yamltest.RequireSourceError(t, err).Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 9), at)
	})

	t.Run("binds the error of the reference at the field", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n$schema: ./s.json\n", niceyaml.WithName("app.yaml"))

		_, err := reg.Lookup(t.Context(), source.Documents()[0])
		require.ErrorIs(t, err, schema.ErrNoBaseDir)
		assert.True(t, strings.HasPrefix(err.Error(), "app.yaml:2:10: "), "got %q", err.Error())
	})

	t.Run("another document binds without a position", func(t *testing.T) {
		t.Parallel()

		base := niceyaml.NewSourceFromString("a: 1\n$schema: ./missing.json\n",
			niceyaml.WithName("base.yaml"), niceyaml.WithFilePath(app))

		ref, err := field.Resolve(t.Context(), base.Documents()[0])
		require.NoError(t, err)

		reused := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(ref))

		other := niceyaml.NewSourceFromString("a: 1\n$schema: ./missing.json\n", niceyaml.WithName("other.yaml"))

		_, err = reused.Lookup(t.Context(), other.Documents()[0])
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.True(t, strings.HasPrefix(err.Error(), "other.yaml: load schema: "), "got %q", err.Error())
	})

	t.Run("replaces an earlier place", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n",
			niceyaml.WithName("app.yaml"), niceyaml.WithFilePath(app))
		doc := source.Documents()[0]

		ref := schema.File(filepath.Join(dir, "missing.json")).
			NamedAt(doc, niceyaml.AtPath(paths.Doc().Child("a"))).
			NamedAt(doc, niceyaml.AtPath(paths.Doc().Child("b")))

		_, err := schema.NewRegistry(schema.WithResolvers(ref)).Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.True(t, strings.HasPrefix(err.Error(), "app.yaml:2:4: "), "got %q", err.Error())
	})

	t.Run("nil document panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Ref.NamedAt: doc is nil", func() {
			schema.URL("https://example.com/s.json").NamedAt(nil)
		})
	})
}

func TestResolverFunc(t *testing.T) {
	t.Parallel()

	// A resolver that names a schema per kind and reports ErrNoMatch for
	// documents without one.
	r := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
		node, err := doc.At(kindPath)
		if err != nil {
			return schema.Ref{}, schema.ErrNoMatch
		}

		kind, err := node.Decode[string](ctx)
		if err != nil {
			return schema.Ref{}, schema.ErrNoMatch
		}

		return loading(kind+".json", []byte(`{"title": "`+kind+`"}`), nil), nil
	})

	tcs := map[string]struct {
		input    string
		wantKey  string
		wantData string
		err      error
	}{
		"names the schema for a kind": {
			input:    `kind: Deployment`,
			wantKey:  "Deployment.json",
			wantData: `{"title": "Deployment"}`,
		},
		"reports no match without a kind": {
			input: `name: x`,
			err:   schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			ref, err := r.Resolve(t.Context(), doc)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantKey, ref.Key())

			data, err := schema.NewRegistry().Load(t.Context(), ref)
			require.NoError(t, err)
			assert.Equal(t, tc.wantData, string(data))
		})
	}
}

func TestLoadable(t *testing.T) {
	t.Parallel()

	t.Run("names the key and loads the bytes", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("config.json", func(_ context.Context) ([]byte, error) {
			return []byte(`{"type": "object"}`), nil
		})

		assert.Equal(t, "config.json", ref.Key())

		data, err := schema.NewRegistry().Load(t.Context(), ref)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
	})

	t.Run("empty key panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Loadable: key is empty", func() {
			schema.Loadable("", func(_ context.Context) ([]byte, error) { return nil, nil })
		})
	})

	t.Run("nil load panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Loadable: load is nil", func() {
			schema.Loadable("config.json", nil)
		})
	})
}
