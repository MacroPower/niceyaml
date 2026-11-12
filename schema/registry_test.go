package schema_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/capture"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/matcher"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
)

// lookup builds a registry that fetches with client and holds r as its
// one resolver, and looks up the schema for a document r never reads. It
// returns the Lookup error, so a test checks which client fetched and what
// the fetch reported.
func lookup(t *testing.T, client *http.Client, r schema.Resolver) error {
	t.Helper()

	reg := schema.NewRegistry(
		schema.WithHTTPClient(client),
		schema.WithResolvers(r),
	)

	_, err := reg.Lookup(t.Context(), document(t))

	return err //nolint:wrapcheck // Tests inspect the registry's own error.
}

// checkInput returns what [schema.Registry.Check] of reg reports for the
// first document of input.
func checkInput(t *testing.T, reg *schema.Registry, input string) error {
	t.Helper()

	doc := yamltest.FirstDocument(t, input)

	return reg.Check(t.Context(), doc) //nolint:wrapcheck // Tests inspect the registry's own error.
}

// requireAccepts fails the test unless reg accepts the document input.
func requireAccepts(t *testing.T, reg *schema.Registry, input string) {
	t.Helper()

	require.NoError(t, checkInput(t, reg, input))
}

// requireRejects fails the test unless reg rejects the document input
// with a violation whose text holds want. The document is at fault for a
// violation, so the error does not wrap [schema.ErrValidate].
func requireRejects(t *testing.T, reg *schema.Registry, input, want string) {
	t.Helper()

	err := checkInput(t, reg, input)
	require.ErrorContains(t, err, want)
	require.NotErrorIs(t, err, schema.ErrValidate)
}

// requireLoadError fails the test unless err is a failed load, which
// wraps [schema.ErrLoad], and wraps cause too.
func requireLoadError(t *testing.T, err, cause error) {
	t.Helper()

	require.ErrorIs(t, err, schema.ErrLoad)
	require.ErrorIs(t, err, cause)
}

// requireRefs returns the option that sets whether a registry compiles a
// schema only when each $ref to another document resolves, as
// [schema.WithRequireRefs] sets it for one compile.
func requireRefs(required bool) schema.RegistryOption {
	return schema.WithCompileOptions(schema.WithRequireRefs(required))
}

// addPassword returns rawURL with the user "user" and the password
// "secret" in front of its host.
func addPassword(rawURL string) string {
	return strings.Replace(rawURL, "://", "://user:secret@", 1)
}

// rootFS returns the file system of an [os.Root] opened on dir. The Root
// stays open until the test ends.
func rootFS(t *testing.T, dir string) fs.FS {
	t.Helper()

	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })

	return root.FS()
}

var (
	// Path helpers for tests.
	kindPath = paths.Current().Child("kind")

	// The two settings of [schema.WithRequireRefs] for a registry, each
	// with the sentinel that a document fails with when its schema holds a
	// $ref the registry cannot resolve. A registry that requires refs
	// fails to load the schema. One that does not compiles it, and the
	// validation that reaches the $ref fails.
	refModes = map[string]struct {
		err     error
		require bool
	}{
		"refs required":     {require: true, err: schema.ErrLoad},
		"refs not required": {require: false, err: schema.ErrValidate},
	}
)

// requireRefModes checks input under a registry of each of refModes,
// built with opts after the mode, in a parallel subtest per mode. Each
// check must fail with the sentinel of its mode and a message that holds
// want.
func requireRefModes(t *testing.T, input, want string, opts ...schema.RegistryOption) {
	t.Helper()

	for name, mode := range refModes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(append([]schema.RegistryOption{requireRefs(mode.require)}, opts...)...)

			err := checkInput(t, reg, input)
			require.ErrorIs(t, err, mode.err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

// loading returns a ref that names key and whose Load returns data and
// err.
func loading(key string, data []byte, err error) schema.Ref {
	return schema.Loadable(key, func(context.Context) ([]byte, error) {
		return data, err
	})
}

// countingLoader returns a ref that names key, serves data, and counts
// how many times its Load runs.
func countingLoader(key string, data []byte) (schema.Ref, *atomic.Int32) {
	var loads atomic.Int32

	r := schema.Loadable(key, func(_ context.Context) ([]byte, error) {
		loads.Add(1)

		return data, nil
	})

	return r, &loads
}

// A gatedContext is a context whose Done method waits for gate to close.
// A test uses it to hold a caller of [schema.Registry.Schema] after the
// caller joins a load and before it waits on the result.
type gatedContext struct {
	context.Context

	gate <-chan struct{}
}

// withGate returns a copy of ctx whose Done method waits for gate to
// close.
func withGate(ctx context.Context, gate <-chan struct{}) context.Context {
	return gatedContext{Context: ctx, gate: gate}
}

// Done waits for gate to close and returns the done channel of the
// wrapped context.
func (c gatedContext) Done() <-chan struct{} {
	<-c.gate

	return c.Context.Done()
}

// pastDeadline returns a context whose deadline has already passed, so
// its error is [context.DeadlineExceeded].
func pastDeadline(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now())
	t.Cleanup(cancel)

	return ctx
}

// countingHandler returns a handler that answers as next does, and a
// function that returns how many requests the handler has answered for
// each path.
func countingHandler(next http.Handler) (http.Handler, func() map[string]int) {
	var (
		mu   sync.Mutex
		hits = map[string]int{}
	)

	count := func(path string) {
		mu.Lock()
		defer mu.Unlock()

		hits[path]++
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count(r.URL.Path)
		next.ServeHTTP(w, r)
	})

	return handler, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()

		return maps.Clone(hits)
	}
}

// An openCountingFS is a file system that counts the files a registry
// opens in files. It has no method but Open, so every read goes through
// the count.
type openCountingFS struct {
	files fstest.MapFS
	opens atomic.Int32
}

func (f *openCountingFS) Open(name string) (fs.File, error) {
	f.opens.Add(1)

	return f.files.Open(name) //nolint:wrapcheck // The test file system returns the error of the one it wraps.
}

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)

	t.Run("first match wins", func(t *testing.T) {
		t.Parallel()

		// The second resolver matches everything, but the lookup never
		// reaches it.
		fallback, fallbackLoads := countingLoader("fallback.json", schemaData)

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(
				matcher.Content(kindPath, "Deployment"),
				schema.Embedded(schemaData),
			),
			fallback,
		))

		m, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Deployment"))
		require.NoError(t, err)
		require.NotNil(t, m.Schema)
		assert.Equal(t, int32(0), fallbackLoads.Load())
	})

	t.Run("returns the ref that named the schema", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		schemaPath := filepath.Join(dir, "schema.json")
		require.NoError(t, os.WriteFile(schemaPath, schemaData, 0o600))

		bundle := fstest.MapFS{"schemas/defs.json": {Data: schemaData}}
		compiled := compileSchema(t, `{"type": "object"}`)

		tcs := map[string]struct {
			resolver schema.Resolver
			doc      string
			wantKey  string
			wantName string
		}{
			"file": {
				resolver: schema.File(schemaPath),
				doc:      "kind: Service\n",
				wantKey:  fileURL(t, schemaPath),
				wantName: fileURL(t, schemaPath),
			},
			"file system": {
				resolver: schema.FileFS(bundle, "schemas/defs.json"),
				doc:      "kind: Service\n",
				wantKey:  "file:///schemas/defs.json",
				wantName: "schemas/defs.json",
			},
			"loadable": {
				resolver: loading("app-schema", schemaData, nil),
				doc:      "kind: Service\n",
				wantKey:  "app-schema",
				wantName: "app-schema",
			},
			"directive": {
				resolver: schema.Directive(),
				doc:      yamltest.SchemaDirective(schemaPath, "kind: Service\n"),
				wantKey:  fileURL(t, schemaPath),
				wantName: fileURL(t, schemaPath),
			},
			"compiled schema": {
				resolver: compiled,
				doc:      "kind: Service\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(
					schema.WithAccess(schema.Unrestricted()),
					schema.WithResolvers(
						schema.When(matcher.Content(kindPath, "Deployment"), compileSchema(t, `false`)),
						tc.resolver,
					),
				)

				m, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, tc.doc))
				require.NoError(t, err)
				require.NotNil(t, m.Schema)
				assert.Equal(t, tc.wantKey, m.Ref.Key())
				assert.Equal(t, tc.wantName, m.Ref.Name())

				fromRef, err := reg.Schema(t.Context(), m.Ref)
				require.NoError(t, err)
				assert.Same(t, m.Schema, fromRef)
			})
		}
	})

	t.Run("ref loads and invalidates the chosen schema", func(t *testing.T) {
		t.Parallel()

		ref, loads := countingLoader("app-schema", schemaData)
		reg := schema.NewRegistry(schema.WithResolvers(ref))
		doc := yamltest.FirstDocument(t, "kind: Service\n")

		m, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(1), loads.Load())

		data, err := reg.Load(t.Context(), m.Ref)
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)

		// The lookup after the release compiles the schema again.
		before := loads.Load()

		reg.Invalidate(m.Ref)

		again, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.NotSame(t, m.Schema, again.Schema)
		assert.Equal(t, before+1, loads.Load())
	})

	t.Run("loader alone applies to every document", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaData)))

		for _, input := range []string{`kind: Deployment`, `kind: Service`, `other: value`} {
			_, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, input))
			require.NoError(t, err)
		}
	})

	t.Run("empty registry matches nothing", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		_, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Service"))
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("a guard that refuses the aliases of the document binds at the alias", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		input := yamltest.AliasLevels(7) + yamltest.AliasKey("kind", 7)

		_, err := reg.Lookup(t.Context(), yamltest.FirstDocumentWithPath(t, input, "app.yaml"))
		require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
		require.ErrorIs(t, err, schema.ErrResolve)

		got, ok := yamltest.RequireSourceError(t, err).Position()
		require.True(t, ok)
		assert.Equal(t, position.New(10, 4), got)
	})

	t.Run("variadic Register keeps order", func(t *testing.T) {
		t.Parallel()

		first, firstLoads := countingLoader("first.json", schemaData)
		second, secondLoads := countingLoader("second.json", schemaData)

		reg := schema.NewRegistry(schema.WithResolvers(first, second))

		_, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Deployment"))
		require.NoError(t, err)
		assert.Equal(t, int32(1), firstLoads.Load())
		assert.Equal(t, int32(0), secondLoads.Load())
	})

	t.Run("a resolver load error that wraps ErrNoMatch ends the lookup", func(t *testing.T) {
		t.Parallel()

		errGone := fmt.Errorf("%w: schema is gone", schema.ErrNoMatch)

		// The resolver loads its schema through a registry of its own and
		// returns the load error of that registry.
		other := schema.NewRegistry()
		loader := schema.ResolverFunc(func(ctx context.Context, _ *niceyaml.Node) (schema.Ref, error) {
			s, err := other.Schema(ctx, loading("gone.json", nil, errGone))
			if err != nil {
				return schema.Ref{}, err //nolint:wrapcheck // The test returns the load error as it is.
			}

			return s.Ref(), nil
		})

		fallback, fallbackLoads := countingLoader("fallback.json", schemaData)
		reg := schema.NewRegistry(schema.WithResolvers(loader, fallback))

		_, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Service"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, errGone)
		require.NotErrorIs(t, err, schema.ErrResolve)
		assert.Equal(t, `load schema: "gone.json": no matching schema: schema is gone`, err.Error())
		assert.False(t, niceyaml.IsInvalid(err))
		assert.Zero(t, fallbackLoads.Load())
	})

	t.Run("a load error is no fault wherever it goes", func(t *testing.T) {
		t.Parallel()

		errGone := errors.New("schema is gone")
		reg := schema.NewRegistry(schema.WithResolvers(loading("gone.json", nil, errGone)))
		doc := yamltest.FirstDocument(t, "kind: Service")

		// The error comes inside Unchecked, so a value check that returns
		// it as it is does not report a fault, and neither does a check of
		// the document.
		var data map[string]any

		err := doc.DecodeInto(t.Context(), &data,
			niceyaml.WithValueCheck(func(ctx context.Context, _ *map[string]any) error {
				_, err := reg.Lookup(ctx, doc)

				return err //nolint:wrapcheck // The test returns the lookup error as it is.
			}))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, errGone)
		assert.False(t, niceyaml.IsInvalid(err))

		err = doc.Validate(t.Context(), reg)
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.False(t, niceyaml.IsInvalid(err))

		err = doc.Validate(t.Context(), niceyaml.MultiValidator(reg, niceyaml.NoValidator()))
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.False(t, niceyaml.IsInvalid(err))
	})
}

func TestRegistry_Resolve(t *testing.T) {
	t.Parallel()

	const nameSchema = `{"properties": {"name": {"type": "string"}}}`

	t.Run("a confined registry serves the directives of another", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		tenant := filepath.Join(base, "tenant")

		yamltest.WriteFiles(t, base, map[string]string{
			"tenant/schema.json": nameSchema,
			"prog/schema.json":   `{"required": ["kind"]}`,
		})

		client, requests := recordingClient(anySchema)

		untrusted := schema.NewRegistry(
			schema.WithHTTPClient(client),
			withAccess(t, allowRoot(t, tenant)),
			schema.WithResolvers(schema.Directive()),
		)

		// The program's own schema lies outside the root of the confined
		// registry.
		reg := schema.NewRegistry(schema.WithResolvers(
			untrusted,
			schema.File(filepath.Join(base, "prog", "schema.json")),
		))

		// The subtests run after this function returns, so the check waits
		// for them.
		t.Cleanup(func() { assert.Empty(t, requests()) })

		tcs := map[string]struct {
			input string
			err   error
			want  string
			// Whether the document is at fault.
			invalid bool
		}{
			"directive inside the root": {
				input:   yamltest.SchemaDirective("./schema.json", "name: 5\n"),
				want:    `expected "string", got "integer"`,
				invalid: true,
			},
			"directive outside the root": {
				input: yamltest.SchemaDirective("../prog/schema.json", "kind: App\n"),
				err:   schema.ErrFileNotAllowed,
				want:  "file not allowed: not under " + tenant,
			},
			"directive that names a URL": {
				input: yamltest.SchemaDirective("https://evil.test/s.json", "kind: App\n"),
				err:   schema.ErrURLNotAllowed,
			},
			"no directive and no kind": {
				input:   "name: x\n",
				want:    `missing required property "kind"`,
				invalid: true,
			},
			"no directive": {
				input: "kind: App\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, tc.input, filepath.Join(tenant, "app.yaml"))

				err := reg.Check(t.Context(), doc)
				if tc.err == nil && tc.want == "" {
					require.NoError(t, err)

					return
				}

				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				}

				require.ErrorContains(t, err, tc.want)
				require.NotErrorIs(t, err, schema.ErrResolve)
				assert.NotContains(t, err.Error(), "resolve schema")
				assert.Equal(t, tc.invalid, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("names the schema its lookup finds", func(t *testing.T) {
		t.Parallel()

		inner := schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(nameSchema))))
		doc := yamltest.FirstDocument(t, "name: x\n")

		found, err := inner.Lookup(t.Context(), doc)
		require.NoError(t, err)

		ref, err := inner.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, found.Schema, ref.Schema())
	})

	t.Run("keeps the name of the schema it chose", func(t *testing.T) {
		t.Parallel()

		bundle := fstest.MapFS{"schemas/defs.json": {Data: []byte(nameSchema)}}
		inner := schema.NewRegistry(schema.WithResolvers(schema.FileFS(bundle, "schemas/defs.json")))
		outer := schema.NewRegistry(schema.WithResolvers(inner))
		doc := yamltest.FirstDocument(t, "name: x\n")

		ref, err := inner.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, "schemas/defs.json", ref.Name())
		assert.Equal(t, "schemas/defs.json", ref.String())
		assert.Empty(t, ref.Key())

		found, err := inner.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, found.Schema, ref.Schema())

		m, err := outer.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, found.Schema, m.Schema)
		assert.Equal(t, "schemas/defs.json", m.Ref.Name())
		assert.Empty(t, m.Ref.Key())

		// The outer registry does not load anything for the Ref, so the
		// bytes stay with the registry that chose the schema.
		_, err = outer.Load(t.Context(), m.Ref)
		require.ErrorIs(t, err, schema.ErrLoad)

		// The outer registry returns the schema that the Ref carries,
		// and Invalidate ignores the Ref despite its file name.
		fromRef, err := outer.Schema(t.Context(), m.Ref)
		require.NoError(t, err)
		assert.Same(t, found.Schema, fromRef)

		outer.Invalidate(m.Ref)

		again, err := outer.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, found.Schema, again.Schema)

		data, err := inner.Load(t.Context(), found.Ref)
		require.NoError(t, err)
		assert.JSONEq(t, nameSchema, string(data))
	})

	t.Run("names a compiled schema without a name", func(t *testing.T) {
		t.Parallel()

		inner := schema.NewRegistry(schema.WithResolvers(compileSchema(t, nameSchema)))

		ref, err := inner.Resolve(t.Context(), yamltest.FirstDocument(t, "name: x\n"))
		require.NoError(t, err)
		require.NotNil(t, ref.Schema())
		assert.Empty(t, ref.Name())
		assert.Empty(t, ref.Key())
	})

	t.Run("lists the reasons of its resolvers when it declines", func(t *testing.T) {
		t.Parallel()

		errNoKind := fmt.Errorf("%w: no kind", schema.ErrNoMatch)
		errNoLabel := fmt.Errorf("%w: no label", schema.ErrNoMatch)

		inner := schema.NewRegistry(
			schema.WithAccess(schema.Unrestricted()),
			schema.WithResolvers(schema.Directive(), failing(errNoKind)),
		)
		reg := schema.NewRegistry(schema.WithResolvers(inner, failing(errNoLabel)))

		doc := yamltest.FirstDocumentWithPath(t, "kind: Service\n", "app.yaml")

		_, err := inner.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)

		_, err = reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)
		require.ErrorIs(t, err, errNoKind)
		require.ErrorIs(t, err, errNoLabel)
		require.NotErrorIs(t, err, schema.ErrResolve)
		assert.Equal(t, "app.yaml: no matching schema", err.Error())

		wantTree := stringtest.JoinLF(
			"app.yaml: no matching schema",
			"|-- no schema directive",
			"|-- no kind",
			"`-- no label",
		)
		assert.Equal(t, wantTree, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
	})

	t.Run("a resolver around a registry decides how its decline reads", func(t *testing.T) {
		t.Parallel()

		errNoLabel := fmt.Errorf("%w: no label", schema.ErrNoMatch)

		inner := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

		tcs := map[string]struct {
			resolver schema.Resolver
			// The lines of the rendered error between the message and the
			// reason of the second resolver.
			want []string
		}{
			"guard that passes the decline through": {
				resolver: schema.When(matcher.Content(kindPath, "Service"), inner),
				want:     []string{"|-- no schema directive"},
			},
			"resolver that adds context": {
				resolver: schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
					ref, err := inner.Resolve(ctx, doc)
					if err != nil {
						return schema.Ref{}, fmt.Errorf("tenant acme: %w", err)
					}

					return ref, nil
				}),
				want: []string{"|-- tenant acme: no matching schema", "|   `-- no schema directive"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(schema.WithResolvers(tc.resolver, failing(errNoLabel)))

				_, err := reg.Lookup(t.Context(), yamltest.FirstDocumentWithPath(t, "kind: Service\n", "app.yaml"))
				require.ErrorIs(t, err, schema.ErrNoMatch)
				require.ErrorIs(t, err, schema.ErrNoDirective)
				require.ErrorIs(t, err, errNoLabel)

				lines := slices.Concat([]string{"app.yaml: no matching schema"}, tc.want, []string{"`-- no label"})
				wantTree := stringtest.JoinLF(lines...)
				assert.Equal(t, wantTree, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
			})
		}
	})

	t.Run("a registry that requires no schema passes what the other declines", func(t *testing.T) {
		t.Parallel()

		client, requests := recordingClient(anySchema)

		untrusted := schema.NewRegistry(
			schema.WithHTTPClient(client),
			withAccess(t),
			schema.WithResolvers(schema.Directive()),
		)

		reg := schema.NewRegistry(schema.WithRequireSchema(false), schema.WithResolvers(untrusted))

		requireAccepts(t, reg, "name: 5\n")

		// A directive the access refuses still fails the document.
		err := checkInput(t, reg, yamltest.SchemaDirective("https://evil.test/s.json", "name: 5\n"))
		requireLoadError(t, err, schema.ErrURLNotAllowed)
		assert.Empty(t, requests())
	})

	t.Run("a failure ends the lookup of the registry that asked", func(t *testing.T) {
		t.Parallel()

		errBroken := errors.New("broken resolver")
		errGone := fmt.Errorf("%w: schema is gone", schema.ErrNoMatch)

		tcs := map[string]struct {
			inner schema.Resolver
			// The sentinels the error of the outer lookup matches.
			errs []error
			// The start of the message, which names the failed step only
			// one time.
			want     string
			resolves int
		}{
			"resolver error": {
				inner:    failing(errBroken),
				errs:     []error{schema.ErrResolve, errBroken},
				want:     "resolve schema: broken resolver",
				resolves: 1,
			},
			"load error that wraps ErrNoMatch": {
				inner: loading("gone.json", nil, errGone),
				errs:  []error{schema.ErrLoad, schema.ErrNoMatch, errGone},
				want:  `load schema: "gone.json": no matching schema: schema is gone`,
			},
			"compile error": {
				inner: schema.Embedded([]byte(`{"type": 5}`)),
				errs:  []error{schema.ErrCompile},
				want:  "compile schema: ",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				fallback, fallbackLoads := countingLoader("fallback.json", []byte(`{}`))

				inner := schema.NewRegistry(schema.WithResolvers(tc.inner))
				reg := schema.NewRegistry(schema.WithResolvers(inner, fallback))

				err := reg.Check(t.Context(), yamltest.FirstDocument(t, "kind: Service\n"))

				for _, want := range tc.errs {
					require.ErrorIs(t, err, want)
				}

				assert.True(t, strings.HasPrefix(err.Error(), tc.want), err.Error())
				assert.Equal(t, tc.resolves, strings.Count(err.Error(), "resolve schema"), err.Error())
				assert.Zero(t, fallbackLoads.Load())
			})
		}
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		inner := schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(nameSchema))))
		doc := yamltest.UnparsedDocument(t, "kind: [\n")

		_, err := inner.Resolve(t.Context(), doc)
		assert.Same(t, doc.Err(), err)
	})

	t.Run("a scoped node fails", func(t *testing.T) {
		t.Parallel()

		inner := schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(nameSchema))))
		doc := yamltest.FirstDocument(t, "spec:\n  name: x\n")

		_, err := inner.Resolve(t.Context(), yamltest.At(t, doc, paths.Current().Child("spec")))
		require.ErrorIs(t, err, niceyaml.ErrScopedNode)
	})
}

func TestRegistry_Check_ScopedNode(t *testing.T) {
	t.Parallel()

	// The resolvers read the file path, the preamble, and the content of a
	// whole document, so Check and Lookup both refuse a scoped Node
	// rather than check the document around it or return its schema for a
	// single node.
	schemaData := []byte(`{
		"type": "object",
		"properties": {
			"kind": {"type": "string"},
			"spec": {"type": "object", "properties": {"replicas": {"type": "integer"}}}
		}
	}`)
	reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaData)))

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		kind: Deployment
		spec:
		  replicas: 1
	`))
	spec := yamltest.At(t, doc, paths.Current().Child("spec"))

	t.Run("refuses a scoped node", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			check func(ctx context.Context, n *niceyaml.Node) error
		}{
			"Check": {check: reg.Check},
			"Lookup": {
				check: func(ctx context.Context, n *niceyaml.Node) error {
					_, err := reg.Lookup(ctx, n)

					return err //nolint:wrapcheck // The test inspects the registry's own error.
				},
			},
			"Validate": {
				check: func(ctx context.Context, n *niceyaml.Node) error {
					return n.Validate(ctx, reg)
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.check(t.Context(), spec)
				require.ErrorIs(t, err, niceyaml.ErrScopedNode)
				require.EqualError(t, err,
					"registry needs a whole document: node is not the root of its document: $.spec")

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, doc.Source(), bound.Source())

				// The refusal is about the call, so it binds to the root
				// of the document and points at no value.
				assert.Same(t, doc, yamltest.BoundNode(t, bound))

				_, ok := bound.Range()
				assert.False(t, ok)
			})
		}
	})

	t.Run("a scoped decode under a registry fails before it decodes", func(t *testing.T) {
		t.Parallel()

		var v struct {
			Replicas int `yaml:"replicas"`
		}

		err := spec.DecodeInto(t.Context(), &v, niceyaml.WithValidator(reg))
		require.ErrorIs(t, err, niceyaml.ErrScopedNode)
		assert.Zero(t, v.Replicas)
	})

	t.Run("a registry that requires no schema refuses a scoped node", func(t *testing.T) {
		t.Parallel()

		lax := schema.NewRegistry(schema.WithRequireSchema(false))

		require.ErrorIs(t, lax.Check(t.Context(), spec), niceyaml.ErrScopedNode)
	})

	t.Run("the root validates once and the nodes decode without it", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, doc.Validate(t.Context(), reg))

		var v struct {
			Replicas int `yaml:"replicas"`
		}

		require.NoError(t, spec.DecodeInto(t.Context(), &v))
		assert.Equal(t, 1, v.Replicas)
	})

	t.Run("Lookup resolves the whole document, whichever Node of it reaches the root", func(t *testing.T) {
		t.Parallel()

		_, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)

		_, err = reg.Lookup(t.Context(), spec.Document())
		require.NoError(t, err)
	})
}

func TestRegistry_Lookup_CancelledContext(t *testing.T) {
	t.Parallel()

	t.Run("canceled before the lookup", func(t *testing.T) {
		t.Parallel()

		// A Ref ignores its context, so the lookup reports the
		// cancellation itself rather than name the schema.
		reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(`{"type":"object"}`))))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := reg.Lookup(ctx, document(t))
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("last resolver cancels and declines", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				cancel()

				return schema.Ref{}, schema.ErrNoMatch
			})),
			schema.WithRequireSchema(false),
		)

		doc := yamltest.FirstDocument(t, "kind: Deployment")

		_, err := reg.Lookup(ctx, doc)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)

		// A registry that does not require a schema passes an unmatched
		// document, but never a canceled lookup.
		require.ErrorIs(t, reg.Check(ctx, doc), context.Canceled)
	})

	t.Run("resolver cancels and fails", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			fail func(ctx context.Context) error
			want string
		}{
			"unrelated error": {
				fail: func(context.Context) error {
					return errors.New("bad directive")
				},
				want: "resolve schema: context canceled",
			},
			"error wraps the context's error": {
				fail: func(ctx context.Context) error {
					return fmt.Errorf("fetch catalog: %w", ctx.Err())
				},
				want: "resolve schema: fetch catalog: context canceled",
			},
			"decline wraps the context's error": {
				fail: func(ctx context.Context) error {
					return fmt.Errorf("%w: %w", schema.ErrNoMatch, ctx.Err())
				},
				want: "resolve schema: context canceled",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "kind: Deployment")

				newRegistry := func(cancel context.CancelFunc) *schema.Registry {
					return schema.NewRegistry(schema.WithResolvers(schema.ResolverFunc(
						func(ctx context.Context, _ *niceyaml.Node) (schema.Ref, error) {
							cancel()

							return schema.Ref{}, tc.fail(ctx)
						},
					)))
				}

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				_, err := newRegistry(cancel).Lookup(ctx, doc)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, schema.ErrResolve)
				require.NotErrorIs(t, err, schema.ErrNoMatch)
				require.EqualError(t, err, tc.want)

				ctx, cancel = context.WithCancel(t.Context())
				defer cancel()

				err = newRegistry(cancel).Check(ctx, doc)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, schema.ErrResolve)
			})
		}
	})

	t.Run("resolver cancels and names a schema", func(t *testing.T) {
		t.Parallel()

		data := []byte(`{"type":"object"}`)

		tcs := map[string]struct {
			ref  schema.Ref
			warm bool
		}{
			"loadable":        {ref: loading("cancel.json", data, nil)},
			"cached loadable": {ref: loading("cancel.json", data, nil), warm: true},
			"embedded":        {ref: schema.Embedded(data)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				var cancelOnResolve atomic.Bool

				reg := schema.NewRegistry(schema.WithResolvers(schema.ResolverFunc(
					func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
						if cancelOnResolve.Load() {
							cancel()
						}

						return tc.ref, nil
					},
				)))

				doc := yamltest.FirstDocument(t, "kind: Deployment")

				if tc.warm {
					_, err := reg.Lookup(ctx, doc)
					require.NoError(t, err)
				}

				cancelOnResolve.Store(true)

				// The outcome does not depend on whether the registry
				// already holds the schema.
				_, err := reg.Lookup(ctx, doc)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, schema.ErrResolve)
				require.NotErrorIs(t, err, schema.ErrLoad)
			})
		}
	})

	t.Run("ends while the schema loads", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// A resolver named the schema, so the ended context is a failed
		// load rather than a failed resolution.
		reg := schema.NewRegistry(schema.WithResolvers(schema.Loadable(
			"cancel.json",
			func(loadCtx context.Context) ([]byte, error) {
				cancel()

				return nil, loadCtx.Err()
			},
		)))

		_, err := reg.Lookup(ctx, yamltest.FirstDocument(t, "kind: Deployment"))
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.NotErrorIs(t, err, schema.ErrResolve)
	})
}

func TestRegistry_Check(t *testing.T) {
	t.Parallel()

	var _ niceyaml.Validator = (*schema.Registry)(nil)

	reg := schema.NewRegistry(schema.WithResolvers(schema.When(
		matcher.Content(kindPath, "Deployment"),
		schema.Embedded([]byte(`{"properties": {"replicas": {"type": "integer"}}}`)),
	)))

	tcs := map[string]struct {
		// The sentinel of the error, if any.
		err   error
		input string
		// The text of the violation, if any.
		want string
	}{
		"document the schema accepts": {input: "kind: Deployment\nreplicas: 3\n"},
		"document the schema rejects": {
			input: "kind: Deployment\nreplicas: many\n",
			want:  `$.replicas: expected "integer", got "string"`,
		},
		"document no resolver applies to": {
			input: "kind: Service\n",
			err:   schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			// A decode that validates with the registry reports what
			// Check reports.
			_, decodeErr := doc.Decode[map[string]any](t.Context(), niceyaml.WithValidator(reg))

			for _, err := range []error{reg.Check(t.Context(), doc), decodeErr} {
				switch {
				case tc.err != nil:
					require.ErrorIs(t, err, tc.err)

				case tc.want != "":
					require.ErrorContains(t, err, tc.want)
					require.NotErrorIs(t, err, schema.ErrValidate)

				default:
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestRegistry_WithRequireSchema(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)

	newRegistry := func() *schema.Registry {
		return schema.NewRegistry(
			schema.WithResolvers(schema.When(
				matcher.Content(kindPath, "Deployment"),
				schema.Embedded(schemaData),
			)),
			schema.WithRequireSchema(false),
		)
	}

	t.Run("accepts a document no resolver applies to", func(t *testing.T) {
		t.Parallel()

		requireAccepts(t, newRegistry(), "kind: Service")
	})

	t.Run("accepts a document without content", func(t *testing.T) {
		t.Parallel()

		requireAccepts(t, newRegistry(), "# only a comment\n")
	})

	t.Run("still validates a document a resolver applies to", func(t *testing.T) {
		t.Parallel()

		requireRejects(t, newRegistry(), "kind: Deployment", `$.kind: expected "number", got "string"`)
	})

	t.Run("still reports other resolver errors", func(t *testing.T) {
		t.Parallel()

		cannotDecide := errors.New("cannot decide")

		reg := schema.NewRegistry(
			schema.WithResolvers(failing(cannotDecide)),
			schema.WithRequireSchema(false),
		)

		err := checkInput(t, reg, "kind: Service")
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorIs(t, err, cannotDecide)
	})

	t.Run("still reports a load error that wraps ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		// The resolver applies, so the failed load is no unmatched
		// document, whatever the load error wraps.
		reg := schema.NewRegistry(
			schema.WithResolvers(loading("k", nil, fmt.Errorf("upstream lookup: %w", schema.ErrNoMatch))),
			schema.WithRequireSchema(false),
		)

		err := checkInput(t, reg, "kind: Service")
		require.ErrorIs(t, err, schema.ErrLoad)
	})

	t.Run("Lookup still reports ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		_, err := newRegistry().Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Service"))
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("runs inside a decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "kind: Service")
		got, err := doc.Decode[map[string]string](t.Context(), niceyaml.WithValidator(newRegistry()))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"kind": "Service"}, got)
	})
}

func TestRegistry_Check_Unparsed(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		reg   *schema.Registry
		err   error
		input string
		opts  []niceyaml.SourceOption
	}{
		"registry that requires a schema": {
			reg:   schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(`{"type": "object"}`)))),
			input: "kind: [\n",
			err:   niceyaml.ErrSyntax,
		},
		"resolver that reads the content": {
			reg: schema.NewRegistry(schema.WithResolvers(schema.When(
				matcher.Content(kindPath, "Deployment"),
				schema.Embedded([]byte(`{"type": "object"}`)),
			))),
			input: "kind: [\n",
			err:   niceyaml.ErrSyntax,
		},
		"registry that accepts a document no resolver applies to": {
			reg: schema.NewRegistry(
				schema.WithAccess(schema.Unrestricted()),
				schema.WithResolvers(schema.Directive()),
				schema.WithRequireSchema(false),
			),
			input: "kind: [\n",
			err:   niceyaml.ErrSyntax,
		},
		"directive that turns validation off": {
			reg: schema.NewRegistry(
				schema.WithAccess(schema.Unrestricted()),
				schema.WithResolvers(schema.Directive()),
			),
			input: yamltest.SchemaDirective("none", "kind: [\n"),
			err:   niceyaml.ErrSyntax,
		},
		"document past the depth limit": {
			reg:   schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(`{"type": "object"}`)))),
			input: "kind: {spec: {name: x}}\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(2)},
			err:   niceyaml.ErrLimit,
		},
		"text past the byte limit": {
			reg:   schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(`{"type": "object"}`)))),
			input: "kind: Deployment\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(8)},
			err:   niceyaml.ErrLimit,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.UnparsedDocument(t, tc.input, tc.opts...)
			require.ErrorIs(t, doc.Err(), tc.err)

			// The document did not parse, so every route to the registry
			// returns the error of the document.
			assert.Same(t, doc.Err(), tc.reg.Check(t.Context(), doc))
			assert.Same(t, doc.Err(), doc.Validate(t.Context(), tc.reg))

			found, err := tc.reg.Lookup(t.Context(), doc)
			assert.Zero(t, found)
			assert.Same(t, doc.Err(), err)
		})
	}
}

func TestRegistry_Caching(t *testing.T) {
	t.Parallel()

	t.Run("validators are cached by URL", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded([]byte(`{"type": "object"}`)),
		)))

		// The first lookup compiles the schema, and the second finds it
		// in the cache.
		v1, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Deployment"))
		require.NoError(t, err)

		v2, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Deployment"))
		require.NoError(t, err)

		assert.Same(t, v1.Schema, v2.Schema)
	})

	t.Run("scheme case does not split the cache", func(t *testing.T) {
		t.Parallel()

		// The client serves the schema from memory and counts every request
		// it sends. Names under .test never resolve, so a fetch that
		// bypasses the client fails rather than going uncounted.
		client, fetches := memoryClient(map[string]string{"/schema.json": `{"type": "object"}`})

		resolve := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			// Each document names the same schema with a different scheme
			// case, as two directives in two files might.
			scheme := "http"
			if doc.DocumentIndex() == 1 {
				scheme = "HTTP"
			}

			return schema.URL(scheme + "://schemas.test/schema.json"), nil
		})

		reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithResolvers(resolve))

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			b: 2
		`))

		for _, doc := range yamltest.Documents(t, source, 2) {
			require.NoError(t, reg.Check(t.Context(), doc))
		}

		assert.Equal(t, int32(1), fetches.Load(), "one schema should be fetched once")
	})

	t.Run("distinct URLs load separately", func(t *testing.T) {
		t.Parallel()

		deployment, deploymentLoads := countingLoader("deployment.json", []byte(`{"type": "object"}`))
		service, serviceLoads := countingLoader("service.json", []byte(`{"type": "object"}`))

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), deployment),
			schema.When(matcher.Content(kindPath, "Service"), service),
		))

		for _, input := range []string{`kind: Deployment`, `kind: Service`, `kind: Deployment`, `kind: Service`} {
			requireAccepts(t, reg, input)
		}

		assert.Equal(t, int32(1), deploymentLoads.Load())
		assert.Equal(t, int32(1), serviceLoads.Load())
	})

	t.Run("zero ref is rejected", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Ref{}, nil
			}),
		))

		_, err := reg.Lookup(t.Context(), document(t))
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorContains(t, err, "names no schema")
	})

	t.Run("reference error is rejected", func(t *testing.T) {
		t.Parallel()

		// A resolver that hands back what FileOrURL returns for a reference
		// that names nothing reports that error.
		reg := schema.NewRegistry(schema.WithResolvers(
			schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
				return schema.FileOrURL(doc.FilePath(), "")
			}),
		))

		_, err := reg.Lookup(t.Context(), document(t))
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorIs(t, err, schema.ErrEmptyPath)
	})

	t.Run("load failure is cached for the retry interval alone", func(t *testing.T) {
		t.Parallel()

		// The bubble's clock stands still while goroutines run, so the
		// second lookup lands inside the retry interval, and each sleep
		// moves the clock past one.
		synctest.Test(t, func(t *testing.T) {
			var loads atomic.Int32

			reg := schema.NewRegistry(
				schema.WithResolvers(schema.Loadable("flaky.json", func(_ context.Context) ([]byte, error) {
					if loads.Add(1) == 1 {
						return nil, errors.New("transient")
					}

					return []byte(`{"type": "object"}`), nil
				})),
			)

			doc := document(t)

			for range 2 {
				_, err := reg.Lookup(t.Context(), doc)
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorContains(t, err, "transient")
			}

			assert.Equal(t, int32(1), loads.Load())

			time.Sleep(time.Minute)

			first, err := reg.Lookup(t.Context(), doc)
			require.NoError(t, err)
			assert.Equal(t, int32(2), loads.Load())

			// The schema that compiled stays for good, where the failure
			// before it lasted one interval.
			time.Sleep(time.Minute)

			second, err := reg.Lookup(t.Context(), doc)
			require.NoError(t, err)
			assert.Same(t, first.Schema, second.Schema)
			assert.Equal(t, int32(2), loads.Load())
		})
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		t.Parallel()

		// No lookup waits for another, so lookups that find the schema in
		// the cache run beside the one that fills it, and the race detector
		// checks the cache reads against that write.
		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded([]byte(`{"type": "object"}`)),
		)))

		// Pre-create documents outside goroutines, because only the test
		// goroutine may run the helper's require calls.
		docs := make([]*niceyaml.Node, 100)
		for i := range docs {
			docs[i] = yamltest.FirstDocument(t, "kind: Deployment")
		}

		var wg sync.WaitGroup

		for _, doc := range docs {
			wg.Go(func() {
				_, err := reg.Lookup(t.Context(), doc)
				assert.NoError(t, err)
			})
		}

		wg.Wait()
	})
}

func TestRegistry_ConcurrentLoad(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// Concurrent lookups for one URL share a single load and compile, and
		// every caller receives the same validator.
		const goroutines = 10

		release := make(chan struct{})

		var loads atomic.Int32

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.Loadable("test.json", func(_ context.Context) ([]byte, error) {
				loads.Add(1)
				<-release // Hold the load open until every caller waits on it.

				return []byte(`{"type": "object"}`), nil
			})),
		)

		// Pre-create documents outside goroutines.
		docs := make([]*niceyaml.Node, goroutines)
		for i := range docs {
			docs[i] = document(t)
		}

		validators := make([]schema.Match, goroutines)
		errs := make([]error, goroutines)

		var wg sync.WaitGroup

		for i := range goroutines {
			wg.Go(func() {
				validators[i], errs[i] = reg.Lookup(t.Context(), docs[i])
			})
		}

		// Wait until every caller is waiting on the one load in flight.
		synctest.Wait()
		close(release)
		wg.Wait()

		for _, err := range errs {
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), loads.Load(), "concurrent lookups should share one load")

		for i := 1; i < goroutines; i++ {
			assert.Same(t, validators[0].Schema, validators[i].Schema)
		}
	})
}

func TestRegistry_SharedLoad(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)

	t.Run("load that times out on its own runs once", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// Load wraps context.DeadlineExceeded the way an http.Client timeout
			// does while every caller's context stays live, so the callers share
			// the one failed load.
			const goroutines = 5

			release := make(chan struct{})

			var loads atomic.Int32

			reg := schema.NewRegistry(
				schema.WithResolvers(schema.Loadable("slow.json", func(_ context.Context) ([]byte, error) {
					loads.Add(1)
					<-release

					return nil, fmt.Errorf("fetch slow.json: %w", context.DeadlineExceeded)
				})),
			)

			doc := document(t)
			errs := make([]error, goroutines)

			var wg sync.WaitGroup

			for i := range goroutines {
				wg.Go(func() {
					_, errs[i] = reg.Lookup(t.Context(), doc)
				})
			}

			// Wait until every caller is waiting on the one load in flight.
			synctest.Wait()
			close(release)
			wg.Wait()

			assert.Equal(t, int32(1), loads.Load(), "callers should share the failed load")

			for _, err := range errs {
				requireLoadError(t, err, context.DeadlineExceeded)
			}
		})
	})

	t.Run("joiner returns when its own deadline passes", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})

			reg := schema.NewRegistry(
				schema.WithResolvers(schema.Loadable("slow.json", func(_ context.Context) ([]byte, error) {
					<-release

					return schemaData, nil
				})),
			)

			doc := document(t)

			var (
				wg        sync.WaitGroup
				leaderErr error
				joinerErr error
			)

			wg.Go(func() {
				_, leaderErr = reg.Lookup(t.Context(), doc)
			})

			// Start the joiner once the leader's load is in flight.
			synctest.Wait()

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			joinerDone := make(chan struct{})

			go func() {
				defer close(joinerDone)

				_, joinerErr = reg.Lookup(ctx, doc)
			}()

			time.Sleep(time.Second)
			synctest.Wait()

			select {
			case <-joinerDone:
			default:
				assert.Fail(t, "joiner should return at its deadline, before the load finishes")
			}

			close(release)
			wg.Wait()
			<-joinerDone

			require.NoError(t, leaderErr)
			requireLoadError(t, joinerErr, context.DeadlineExceeded)
		})
	})

	t.Run("joiner loads again after the starting caller cancels", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var loads atomic.Int32

			reg := schema.NewRegistry(
				schema.WithResolvers(schema.Loadable("slow.json", func(ctx context.Context) ([]byte, error) {
					if loads.Add(1) == 1 {
						// The first load runs under the starting caller's
						// context and ends with its cancellation.
						<-ctx.Done()

						return nil, fmt.Errorf("fetch slow.json: %w", ctx.Err())
					}

					return schemaData, nil
				})),
			)

			doc := document(t)

			leaderCtx, cancelLeader := context.WithCancel(t.Context())
			defer cancelLeader()

			var (
				wg        sync.WaitGroup
				leaderErr error
				joinerErr error
			)

			wg.Go(func() {
				_, leaderErr = reg.Lookup(leaderCtx, doc)
			})

			synctest.Wait()

			wg.Go(func() {
				_, joinerErr = reg.Lookup(t.Context(), doc)
			})

			// Cancel the leader once the joiner is waiting on its load.
			synctest.Wait()
			cancelLeader()
			wg.Wait()

			requireLoadError(t, leaderErr, context.Canceled)
			require.NoError(t, joinerErr)
			assert.Equal(t, int32(2), loads.Load())
		})
	})

	t.Run("caller whose context ends takes no schema from a later load", func(t *testing.T) {
		t.Parallel()

		// A late caller joins a load, and its deadline passes before it
		// waits on the result. The load fails because its starter cancels,
		// and a joiner with a live context loads again and caches the
		// schema. The late caller then finds both its context and the
		// failed load ready, and the select picks one at random, so the
		// test runs the race enough times to take both picks. Either way
		// the late caller reports its own deadline rather than the
		// starter's cancellation.
		synctest.Test(t, func(t *testing.T) {
			for range 64 {
				var loads atomic.Int32

				ref := schema.Loadable("slow.json", func(ctx context.Context) ([]byte, error) {
					if loads.Add(1) == 1 {
						<-ctx.Done()

						return nil, fmt.Errorf("fetch slow.json: %w", ctx.Err())
					}

					return schemaData, nil
				})

				reg := schema.NewRegistry()

				starterCtx, cancelStarter := context.WithCancel(t.Context())

				var (
					wg         sync.WaitGroup
					starterErr error
					joinerErr  error
				)

				wg.Go(func() {
					_, starterErr = reg.Schema(starterCtx, ref)
				})

				synctest.Wait()

				wg.Go(func() {
					_, joinerErr = reg.Schema(t.Context(), ref)
				})

				synctest.Wait()

				// The late caller's deadline has already passed, and the gate
				// holds the caller between joining the load and waiting on it
				// until the joiner has loaded again.
				gate := make(chan struct{})
				lateCtx := withGate(pastDeadline(t), gate)
				lateDone := make(chan struct{})

				var (
					late    *schema.Schema
					lateErr error
				)

				go func() {
					defer close(lateDone)

					late, lateErr = reg.Schema(lateCtx, ref)
				}()

				synctest.Wait()
				cancelStarter()
				wg.Wait()

				// Let the failed load reach the late caller before it waits
				// on the result.
				synctest.Wait()
				close(gate)
				<-lateDone

				requireLoadError(t, starterErr, context.Canceled)
				require.NoError(t, joinerErr)
				requireLoadError(t, lateErr, context.DeadlineExceeded)
				require.NotErrorIs(t, lateErr, context.Canceled)
				require.Nil(t, late)
				require.Equal(t, int32(2), loads.Load())
			}
		})
	})

	t.Run("caller whose context ends takes a load that already finished", func(t *testing.T) {
		t.Parallel()

		errBroken := errors.New("broken")

		tcs := map[string]struct {
			data []byte
			err  error
		}{
			"schema":  {data: schemaData},
			"failure": {err: errBroken},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// A late caller joins a load, and its deadline passes before
				// it waits on the result. The load then finishes, so the late
				// caller finds both its context and the result ready, and the
				// select picks one at random. The result counts either way,
				// and the test runs the race enough times to take both picks.
				synctest.Test(t, func(t *testing.T) {
					for range 64 {
						release := make(chan struct{})

						var loads atomic.Int32

						ref := schema.Loadable("slow.json", func(_ context.Context) ([]byte, error) {
							loads.Add(1)
							<-release

							return tc.data, tc.err
						})

						reg := schema.NewRegistry()

						var (
							wg         sync.WaitGroup
							starter    *schema.Schema
							starterErr error
						)

						wg.Go(func() {
							starter, starterErr = reg.Schema(t.Context(), ref)
						})

						synctest.Wait()

						// The gate holds the late caller between joining the load
						// and waiting on it until the load has finished.
						gate := make(chan struct{})
						lateCtx := withGate(pastDeadline(t), gate)
						lateDone := make(chan struct{})

						var (
							late    *schema.Schema
							lateErr error
						)

						go func() {
							defer close(lateDone)

							late, lateErr = reg.Schema(lateCtx, ref)
						}()

						synctest.Wait()
						close(release)
						wg.Wait()

						// Let the result reach the late caller before it waits on
						// it.
						synctest.Wait()
						close(gate)
						<-lateDone

						require.Equal(t, int32(1), loads.Load())
						require.Same(t, starter, late)

						if tc.err == nil {
							require.NoError(t, starterErr)
							require.NoError(t, lateErr)
							require.NotNil(t, late)
						} else {
							require.ErrorIs(t, starterErr, tc.err)
							requireLoadError(t, lateErr, tc.err)
							require.NotErrorIs(t, lateErr, context.DeadlineExceeded)
						}
					}
				})
			})
		}
	})
}

func TestRegistry_DynamicResolver(t *testing.T) {
	t.Parallel()

	// Create schema files for different kinds.
	tmpDir := t.TempDir()
	yamltest.WriteFiles(t, tmpDir, map[string]string{
		"Deployment.json": `{"type": "object", "properties": {"kind": {"const": "Deployment"}}}`,
		"Service.json":    `{"type": "object", "properties": {"kind": {"const": "Service"}}}`,
	})

	reg := schema.NewRegistry(
		schema.WithResolvers(
			schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
				node, err := doc.At(kindPath)
				if err != nil {
					return schema.Ref{}, schema.ErrNoMatch
				}

				kind, err := node.Decode[string](ctx)
				if err != nil || (kind != "Deployment" && kind != "Service") {
					return schema.Ref{}, schema.ErrNoMatch
				}

				return schema.File(filepath.Join(tmpDir, kind+".json")), nil
			}),
		),
	)

	requireAccepts(t, reg, "kind: Deployment")
	requireAccepts(t, reg, "kind: Service")

	// ConfigMap has no schema.
	err := checkInput(t, reg, "kind: ConfigMap")
	require.ErrorIs(t, err, schema.ErrNoMatch)
}

func TestRegistry_OptionSlices(t *testing.T) {
	t.Parallel()

	// The registry compiles each schema on its first lookup and runs a
	// CompileOption only then, so an option that aliased the caller's
	// slice would let a later write change how the next schema compiles.
	// Each option copies its slice when the caller builds it. Asserting
	// formats makes the difference observable, so a registry takes an
	// ipv4 schema and must reject a value that is not an address, unless
	// its case names another input.
	ipv4 := schema.WithResolvers(schema.Embedded([]byte(`{"type": "string", "format": "ipv4"}`)))

	formats := func(on bool) schema.CompileOption {
		return schema.WithJSONSchemaOptions(jsonschema.WithFormats(on))
	}

	tcs := map[string]struct {
		newRegistry func() *schema.Registry
		input       string
		want        string
	}{
		"compile options written after NewRegistry": {
			newRegistry: func() *schema.Registry {
				opts := []schema.CompileOption{formats(true)}
				reg := schema.NewRegistry(schema.WithCompileOptions(opts...), ipv4)
				opts[0] = formats(false)

				return reg
			},
		},
		// A write between WithCompileOptions and NewRegistry changes
		// nothing either.
		"compile options written before NewRegistry": {
			newRegistry: func() *schema.Registry {
				opts := []schema.CompileOption{formats(true)}
				opt := schema.WithCompileOptions(opts...)
				opts[0] = formats(false)

				return schema.NewRegistry(opt, ipv4)
			},
		},
		"JSON Schema options written after NewRegistry": {
			newRegistry: func() *schema.Registry {
				jopts := []jsonschema.ValidateOption{jsonschema.WithFormats(true)}
				reg := schema.NewRegistry(schema.WithCompileOptions(schema.WithJSONSchemaOptions(jopts...)), ipv4)
				jopts[0] = jsonschema.WithFormats(false)

				return reg
			},
		},
		// Each call appends, so the format assertions the first call asks
		// for still reach the compiler after the second call adds to them.
		"compile options of two calls": {
			newRegistry: func() *schema.Registry {
				return schema.NewRegistry(
					schema.WithCompileOptions(formats(true)),
					schema.WithCompileOptions(schema.WithJSONSchemaOptions()),
					ipv4,
				)
			},
		},
		// A nil written to the caller's slice afterwards neither slips past
		// the nil check nor reaches Lookup.
		"resolvers written after WithResolvers": {
			newRegistry: func() *schema.Registry {
				res := []schema.Resolver{schema.Embedded([]byte(`{"type": "string"}`))}
				opt := schema.WithResolvers(res...)
				res[0] = nil

				return schema.NewRegistry(opt)
			},
			input: "key: value",
			want:  `expected "string", got "object"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			requireRejects(t, tc.newRegistry(),
				cmp.Or(tc.input, "not-an-ip"),
				cmp.Or(tc.want, `string does not match format "ipv4"`))
		})
	}
}

func TestRegistry_ErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("resolve error propagates", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(failing(errors.New("cannot decide"))))

		err := reg.Check(t.Context(), document(t))
		require.ErrorIs(t, err, schema.ErrResolve)
		assert.Contains(t, err.Error(), "cannot decide")
	})

	t.Run("load error propagates through Check", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(loading("broken.json", nil, errors.New("disk on fire"))))

		err := reg.Check(t.Context(), document(t))
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.Contains(t, err.Error(), "disk on fire")
		assert.Contains(t, err.Error(), "broken.json")
	})

	t.Run("compile error propagates", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(loading("bad.json", []byte(`{not valid json`), nil)))

		_, err := reg.Lookup(t.Context(), document(t))
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.Contains(t, err.Error(), "bad.json")
	})

	t.Run("a nil resolver panics", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			resolver schema.Resolver
		}{
			"nil interface":    {resolver: nil},
			"nil ResolverFunc": {resolver: schema.ResolverFunc(nil)},
			"nil pointer":      {resolver: (*schema.Schema)(nil)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, "schema.WithResolvers: resolver is nil", func() {
					schema.WithResolvers(schema.Directive(), tc.resolver)
				})
			})
		}
	})
}

func TestRegistry_MultipleDocuments(t *testing.T) {
	t.Parallel()

	deploymentSchema := []byte(
		`{"type": "object", "properties": {"kind": {"const": "Deployment"}}, "required": ["kind"]}`,
	)
	serviceSchema := []byte(`{"type": "object", "properties": {"kind": {"const": "Service"}}, "required": ["kind"]}`)

	reg := schema.NewRegistry(schema.WithResolvers(
		schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded(deploymentSchema)),
		schema.When(matcher.Content(kindPath, "Service"), schema.Embedded(serviceSchema)),
	))

	docs := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
		kind: Deployment
		---
		kind: Service
		---
		kind: ConfigMap
	`)), 3)

	// Each schema takes one kind alone, so a document passes only the
	// schema of its own resolver. No resolver applies to the ConfigMap.
	require.NoError(t, reg.Check(t.Context(), docs[0]))
	require.NoError(t, reg.Check(t.Context(), docs[1]))
	require.ErrorIs(t, reg.Check(t.Context(), docs[2]), schema.ErrNoMatch)
}

func TestRegistry_Validator_ErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	reg := schema.NewRegistry(schema.WithResolvers(schema.When(
		matcher.Content(kindPath, "Deployment"),
		schema.Embedded([]byte(`{"properties": {"replicas": {"type": "integer"}}}`)),
	)))

	// The registry gets the document the decode runs on, whatever options
	// the source and the decode hold, and binds its errors through it.
	refs := yamltest.References("base: &x 1\n")
	ordered := niceyaml.WithYAMLOrderedMaps(true)

	tcs := map[string]struct {
		input  string
		source []niceyaml.SourceOption
		opts   []niceyaml.DecodeOption
		err    error
	}{
		"rejected with references": {
			input:  "kind: Deployment\nreplicas: many\n",
			source: []niceyaml.SourceOption{refs},
		},
		"rejected with ordered maps": {
			input: "kind: Deployment\nreplicas: many\n",
			opts:  []niceyaml.DecodeOption{ordered},
		},
		"unmatched with references": {
			input:  "kind: Service\n",
			source: []niceyaml.SourceOption{refs},
			err:    schema.ErrNoMatch,
		},
		"unmatched with ordered maps": {
			input: "kind: Service\n",
			opts:  []niceyaml.DecodeOption{ordered},
			err:   schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.source...)
			opts := niceyaml.DecodeOptions(append(slices.Clone(tc.opts), niceyaml.WithValidator(reg))...)

			_, decodeErr := doc.Decode[any](t.Context(), opts)
			validateErr := doc.Validate(t.Context(), reg)

			for _, err := range []error{decodeErr, validateErr} {
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				}

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, doc, yamltest.BoundNode(t, bound))
				assert.Same(t, doc, yamltest.BoundDocument(t, bound))
			}
		})
	}
}

func TestRegistry_Lookup_MatcherError(t *testing.T) {
	t.Parallel()

	// The matcher cannot decide on a document whose routing key holds an
	// alias with no anchor, so the lookup stops there rather than routing
	// the document to the next resolver.
	fallbackCalled := false
	reg := schema.NewRegistry(
		schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded([]byte(`{"type":"object"}`))),
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				fallbackCalled = true

				return schema.Embedded([]byte(`{}`)), nil
			}),
		),
		schema.WithRequireSchema(false),
	)

	doc := yamltest.FirstDocument(t, "kind: *missing")

	_, err := reg.Lookup(t.Context(), doc)
	require.ErrorIs(t, err, schema.ErrResolve)
	require.ErrorIs(t, err, paths.ErrAlias)
	require.NotErrorIs(t, err, schema.ErrNoMatch)
	assert.False(t, fallbackCalled)

	// A registry that does not require a schema passes an unmatched
	// document, but never one its matcher could not decide on.
	require.ErrorIs(t, reg.Check(t.Context(), doc), paths.ErrAlias)
}

func TestRegistry_CompiledSchema(t *testing.T) {
	t.Parallel()

	deployment := compileSchema(t, `{"type": "object", "required": ["replicas"]}`)
	service := compileSchema(t, `{"type": "object", "required": ["port"]}`)

	t.Run("a schema is a resolver the registry validates with as it is", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), deployment),
			schema.When(matcher.Content(kindPath, "Service"), service),
		))

		doc := yamltest.FirstDocument(t, "kind: Service\nport: 80\n")

		got, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, service, got.Schema)
		require.NoError(t, reg.Check(t.Context(), doc))

		requireRejects(t, reg, "kind: Deployment\nport: 80\n", `missing required property "replicas"`)
	})

	t.Run("a schema built from a Go type routes the same way", func(t *testing.T) {
		t.Parallel()

		type pod struct {
			Kind  string `json:"kind"`
			Image string `json:"image"`
		}

		generated, err := jsonschema.NewGenerator().GenerateFor[pod](t.Context())
		require.NoError(t, err)

		podSchema := schema.FromJSONSchema(jsonschema.MustCompile(generated))

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Pod"), podSchema),
		))

		got, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Pod\nimage: nginx\n"))
		require.NoError(t, err)
		assert.Same(t, podSchema, got.Schema)

		_, err = reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Job\n"))
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("a resolver returns the ref of a schema", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
				if strings.HasSuffix(doc.FilePath(), ".svc.yaml") {
					return service.Ref(), nil
				}

				return schema.Ref{}, schema.ErrNoMatch
			}),
		))

		doc := yamltest.FirstDocumentWithPath(t, "port: 80\n", "web.svc.yaml")

		got, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, service, got.Schema)
	})

	t.Run("a compiled schema keeps the options it was compiled with", func(t *testing.T) {
		t.Parallel()

		// The registry asserts formats, and the schema compiled without
		// them, so the value passes. The registry compiles nothing here.
		lax := compileSchema(t, `{"type": "string", "format": "ipv4"}`)

		reg := schema.NewRegistry(
			schema.WithCompileOptions(schema.WithJSONSchemaOptions(jsonschema.WithFormats(true))),
			schema.WithResolvers(lax),
		)

		requireAccepts(t, reg, "not-an-ip")
	})
}

func TestRegistry_WithHTTPClient(t *testing.T) {
	t.Parallel()

	schemaURL := serve(t, map[string]string{"/schema.json": `{"type": "object"}`}) + "/schema.json"

	t.Run("a nil client keeps the default", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(
			schema.WithHTTPClient(nil),
			schema.WithResolvers(schema.URL(schemaURL)),
		)

		requireAccepts(t, reg, "a: 1\n")
	})

	t.Run("a client replaces the default", func(t *testing.T) {
		t.Parallel()

		client, requests := yamltest.CountingClient(http.DefaultTransport)

		require.NoError(t, lookup(t, client, schema.URL(schemaURL)))
		assert.Equal(t, int32(1), requests.Load())
	})
}

func TestRegistry_WithRetryAfter(t *testing.T) {
	t.Parallel()

	const (
		// The schema every document of these subtests names. The host
		// resolves nowhere, and a client answers for it from memory.
		schemaURL = "http://schemas.test/s.json"

		// A schema whose $ref names the document /defs.json beside it.
		rootSchema = `{"properties": {"a": {"$ref": "defs.json#/$defs/a"}}}`
	)

	// The registry of each subtest takes the schema of a document from
	// its directive and fetches it from memory, with opts applied after.
	// The handler behind it holds the body of each path in files and
	// answers 404 for any other. The function beside the registry returns
	// the requests the handler has answered, by path.
	newRegistry := func(
		files map[string]string, opts ...schema.RegistryOption,
	) (*schema.Registry, func() map[string]int) {
		handler, requests := countingHandler(schemaHandler(files))

		reg := schema.NewRegistry(append([]schema.RegistryOption{
			schema.WithAccess(schema.Unrestricted()),
			schema.WithHTTPClient(handlerTransport(handler).Client()),
			schema.WithResolvers(schema.Directive()),
		}, opts...)...)

		return reg, requests
	}

	// Five documents that name schemaURL go through reg, one after
	// another, and each must fail with want.
	checkFive := func(t *testing.T, reg *schema.Registry, want error) {
		t.Helper()

		for range 5 {
			err := checkInput(t, reg, yamltest.SchemaDirective(schemaURL, "a: 1\n"))
			require.ErrorIs(t, err, want)
		}
	}

	// A subtest that waits out an interval runs in a synctest bubble. Its
	// clock stands still while goroutines run, so five documents in a row
	// land inside one interval, and a sleep moves the clock past the
	// interval without waiting in real time.
	t.Run("failure costs one attempt per interval", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The sentinel each document fails with.
			err   error
			files map[string]string
			// The requests by path after five documents inside the first
			// interval, and after five more inside the second.
			first  map[string]int
			second map[string]int
			opts   []schema.RegistryOption
		}{
			"schema that does not load": {
				err:    schema.ErrLoad,
				first:  map[string]int{"/s.json": 1},
				second: map[string]int{"/s.json": 2},
			},
			"schema that does not decode": {
				files:  map[string]string{"/s.json": `{"type": 5}`},
				err:    schema.ErrCompile,
				first:  map[string]int{"/s.json": 1},
				second: map[string]int{"/s.json": 2},
			},
			"$ref that does not load": {
				files:  map[string]string{"/s.json": rootSchema},
				err:    schema.ErrLoad,
				first:  map[string]int{"/s.json": 1, "/defs.json": 1},
				second: map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			// The schema compiles without the document, so the registry
			// keeps the schema and fetches the document alone again.
			"$ref that does not load in a schema that compiles without it": {
				files:  map[string]string{"/s.json": rootSchema},
				opts:   []schema.RegistryOption{requireRefs(false)},
				err:    schema.ErrValidate,
				first:  map[string]int{"/s.json": 1, "/defs.json": 1},
				second: map[string]int{"/s.json": 1, "/defs.json": 2},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					reg, requests := newRegistry(tc.files, tc.opts...)

					checkFive(t, reg, tc.err)
					assert.Equal(t, tc.first, requests())

					time.Sleep(time.Minute)

					checkFive(t, reg, tc.err)
					assert.Equal(t, tc.second, requests())
				})
			})
		}
	})

	t.Run("schema that does not compile costs one fetch and one compile", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// The document the $ref names loads, but it holds no subschema
			// at the pointer of the $ref, so each compile fails. Each
			// compile follows a fetch of /s.json, so those fetches count
			// the compiles. The registry keeps the document that loaded,
			// so it fetches /defs.json once.
			reg, requests := newRegistry(map[string]string{
				"/s.json":    `{"$ref": "defs.json#/$defs/missing"}`,
				"/defs.json": `{}`,
			})

			checkFive(t, reg, schema.ErrCompile)
			assert.Equal(t, map[string]int{"/s.json": 1, "/defs.json": 1}, requests())

			time.Sleep(time.Minute)

			checkFive(t, reg, schema.ErrCompile)
			assert.Equal(t, map[string]int{"/s.json": 2, "/defs.json": 1}, requests())
		})
	})

	t.Run("interval", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts []schema.RegistryOption
			want time.Duration
		}{
			"defaults to one minute": {
				want: time.Minute,
			},
			"positive replaces the default": {
				opts: []schema.RegistryOption{schema.WithRetryAfter(20 * time.Millisecond)},
				want: 20 * time.Millisecond,
			},
			"zero keeps the default": {
				opts: []schema.RegistryOption{schema.WithRetryAfter(0)},
				want: time.Minute,
			},
			"negative keeps the default": {
				opts: []schema.RegistryOption{schema.WithRetryAfter(-time.Second)},
				want: time.Minute,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					reg, requests := newRegistry(nil, tc.opts...)

					checkFive(t, reg, schema.ErrLoad)
					assert.Equal(t, map[string]int{"/s.json": 1}, requests())

					// The interval has a nanosecond left.
					time.Sleep(tc.want - time.Nanosecond)

					checkFive(t, reg, schema.ErrLoad)
					assert.Equal(t, map[string]int{"/s.json": 1}, requests())

					time.Sleep(time.Nanosecond)

					checkFive(t, reg, schema.ErrLoad)
					assert.Equal(t, map[string]int{"/s.json": 2}, requests())
				})
			})
		}
	})

	t.Run("failure under a context that ended is not remembered", func(t *testing.T) {
		t.Parallel()

		const defsSchema = `{"$defs": {"a": {"type": "integer"}}}`

		tcs := map[string]struct {
			files map[string]string
			// The path whose first fetch fails with the error of its
			// context.
			held string
			opts []schema.RegistryOption
		}{
			"schema": {
				files: map[string]string{"/s.json": `{"type": "object"}`},
				held:  "/s.json",
			},
			"document a $ref names": {
				files: map[string]string{"/s.json": rootSchema, "/defs.json": defsSchema},
				held:  "/defs.json",
			},
			"document a $ref names in a schema that compiles without it": {
				files: map[string]string{"/s.json": rootSchema, "/defs.json": defsSchema},
				held:  "/defs.json",
				opts:  []schema.RegistryOption{requireRefs(false)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					answer := handlerTransport(schemaHandler(tc.files))
					release := make(chan struct{})

					var fetches atomic.Int32

					// The transport holds the first fetch of the path until
					// release closes, and then fails it with the error of
					// the context it ran under.
					client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
						if r.URL.Path == tc.held && fetches.Add(1) == 1 {
							<-release

							return nil, r.Context().Err()
						}

						return answer(r)
					}).Client()

					ref := schema.URL(schemaURL)
					reg := schema.NewRegistry(append(tc.opts,
						schema.WithHTTPClient(client),
						schema.WithResolvers(ref),
					)...)

					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()

					var (
						wg  sync.WaitGroup
						err error
					)

					wg.Go(func() {
						_, err = reg.Schema(ctx, ref)
					})

					// Cancel once the fetch waits in the transport. The
					// request returns then, and the load fails after it,
					// under the context that ended.
					synctest.Wait()
					cancel()
					wg.Wait()
					close(release)
					synctest.Wait()

					requireLoadError(t, err, context.Canceled)

					// No interval has passed, so a remembered failure would
					// fail this document too.
					requireAccepts(t, reg, "a: 1\n")
					assert.Equal(t, int32(2), fetches.Load())
				})
			})
		}
	})

	t.Run("remembered failure names the Ref of each request", func(t *testing.T) {
		t.Parallel()

		// Two spellings of one path in one file system share a cache key,
		// so the second request gets the failure of the first, and its
		// message names the path the second Ref spells.
		fsys := &openCountingFS{files: yamltest.MapFS(nil)}
		reg := schema.NewRegistry()

		_, err := reg.Schema(t.Context(), schema.FileFS(fsys, "schemas/gone.json"))
		requireLoadError(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), `load schema: "schemas/gone.json": `), err.Error())

		opens := fsys.opens.Load()
		require.Positive(t, opens)

		_, err = reg.Schema(t.Context(), schema.FileFS(fsys, "./schemas/gone.json"))
		requireLoadError(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), `load schema: "./schemas/gone.json": `), err.Error())
		assert.Equal(t, opens, fsys.opens.Load())
	})
}

func TestRegistry_WithRefreshAfter(t *testing.T) {
	t.Parallel()

	const (
		// The schema each registry of these subtests resolves every
		// document to. The host resolves nowhere, and a client answers for
		// it from memory.
		rootURL = "http://schemas.test/root.json"

		// A schema whose $ref names the document /defs.json beside it.
		rootSchema = `{"properties": {"a": {"$ref": "defs.json#/$defs/a"}}}`

		// Two versions of /defs.json. The first accepts "a: 1" and the
		// second rejects it.
		defsInteger = `{"$defs": {"a": {"type": "integer"}}}`
		defsString  = `{"$defs": {"a": {"type": "string"}}}`
	)

	// A host serves the files that it holds from memory and counts the
	// requests by path. A test swaps its files to change a schema, or
	// empties them to take the host down.
	type host struct {
		requests func() map[string]int
		files    atomic.Pointer[map[string]string]
		client   *http.Client
	}

	newHost := func(files map[string]string) *host {
		h := &host{}
		h.files.Store(&files)

		var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			schemaHandler(*h.files.Load()).ServeHTTP(w, r)
		})

		handler, h.requests = countingHandler(handler)
		h.client = handlerTransport(handler).Client()

		return h
	}

	// The registry of each subtest fetches through h and resolves every
	// document to ref, with opts applied after.
	newRegistry := func(h *host, ref schema.Ref, opts ...schema.RegistryOption) *schema.Registry {
		return schema.NewRegistry(append([]schema.RegistryOption{
			schema.WithHTTPClient(h.client),
			schema.WithResolvers(ref),
		}, opts...)...)
	}

	// A subtest that waits out an interval runs in a synctest bubble, so a
	// sleep moves the clock without waiting in real time.
	t.Run("fetches a URL schema and its $ref documents again", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			h := newHost(map[string]string{"/root.json": rootSchema, "/defs.json": defsInteger})
			reg := newRegistry(h, schema.URL(rootURL), schema.WithRefreshAfter(time.Hour))

			requireAccepts(t, reg, "a: 1\n")
			assert.Equal(t, map[string]int{"/root.json": 1, "/defs.json": 1}, h.requests())

			h.files.Store(&map[string]string{"/root.json": rootSchema, "/defs.json": defsString})

			// The interval has a nanosecond left.
			time.Sleep(time.Hour - time.Nanosecond)

			requireAccepts(t, reg, "a: 1\n")
			assert.Equal(t, map[string]int{"/root.json": 1, "/defs.json": 1}, h.requests())

			time.Sleep(time.Nanosecond)

			requireRejects(t, reg, "a: 1\n", "string")
			requireAccepts(t, reg, "a: x\n")
			assert.Equal(t, map[string]int{"/root.json": 2, "/defs.json": 2}, h.requests())
		})
	})

	t.Run("a file schema reads a new copy of the URL its $ref names", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			h := newHost(map[string]string{"/defs.json": defsInteger})
			fsys := &openCountingFS{files: yamltest.MapFS(map[string]string{
				"root.json": `{"properties": {"a": {"$ref": "http://schemas.test/defs.json#/$defs/a"}}}`,
			})}
			reg := newRegistry(h, schema.FileFS(fsys, "root.json"), schema.WithRefreshAfter(time.Hour))

			requireAccepts(t, reg, "a: 1\n")
			assert.Equal(t, map[string]int{"/defs.json": 1}, h.requests())

			opens := fsys.opens.Load()

			h.files.Store(&map[string]string{"/defs.json": defsString})
			time.Sleep(time.Hour)

			// The schema reached the URL, so it compiles again, and the
			// compile reads the file again.
			requireRejects(t, reg, "a: 1\n", "string")
			assert.Equal(t, map[string]int{"/defs.json": 2}, h.requests())
			assert.Greater(t, fsys.opens.Load(), opens)
		})
	})

	t.Run("serves the held copy when the fetch fails", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The files the host holds while the fetch fails.
			down map[string]string
			// The requests by path after the first failed refresh, and
			// after the second, one retry interval later.
			first  map[string]int
			second map[string]int
		}{
			"schema": {
				first:  map[string]int{"/root.json": 2, "/defs.json": 1},
				second: map[string]int{"/root.json": 3, "/defs.json": 1},
			},
			// The schema compiles again with the copy of the document
			// that the registry holds, and falls due with that document.
			"document a $ref names": {
				down:   map[string]string{"/root.json": rootSchema},
				first:  map[string]int{"/root.json": 2, "/defs.json": 2},
				second: map[string]int{"/root.json": 3, "/defs.json": 3},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					up := map[string]string{"/root.json": rootSchema, "/defs.json": defsInteger}
					h := newHost(up)
					reg := newRegistry(h, schema.URL(rootURL),
						schema.WithRefreshAfter(time.Hour),
						schema.WithRetryAfter(time.Minute),
					)

					requireAccepts(t, reg, "a: 1\n")

					h.files.Store(&tc.down)
					time.Sleep(time.Hour)

					for range 5 {
						requireAccepts(t, reg, "a: 1\n")
					}

					assert.Equal(t, tc.first, h.requests())

					time.Sleep(time.Minute)

					requireAccepts(t, reg, "a: 1\n")
					assert.Equal(t, tc.second, h.requests())

					// The host comes back with a new version of the
					// document, and the next retry fetches it.
					h.files.Store(&map[string]string{"/root.json": rootSchema, "/defs.json": defsString})
					time.Sleep(time.Minute)

					requireRejects(t, reg, "a: 1\n", "string")
				})
			})
		}
	})

	t.Run("never by default", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts []schema.RegistryOption
		}{
			"no option":         {},
			"zero interval":     {opts: []schema.RegistryOption{schema.WithRefreshAfter(0)}},
			"negative interval": {opts: []schema.RegistryOption{schema.WithRefreshAfter(-time.Hour)}},
			"later zero interval": {
				opts: []schema.RegistryOption{schema.WithRefreshAfter(time.Hour), schema.WithRefreshAfter(0)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					h := newHost(map[string]string{"/root.json": rootSchema, "/defs.json": defsInteger})
					reg := newRegistry(h, schema.URL(rootURL), tc.opts...)

					requireAccepts(t, reg, "a: 1\n")

					h.files.Store(&map[string]string{"/root.json": rootSchema, "/defs.json": defsString})
					time.Sleep(1000 * time.Hour)

					requireAccepts(t, reg, "a: 1\n")
					assert.Equal(t, map[string]int{"/root.json": 1, "/defs.json": 1}, h.requests())
				})
			})
		}
	})

	t.Run("a schema falls due with a URL document it took from the cache", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			h := newHost(map[string]string{"/root.json": rootSchema, "/defs.json": defsInteger})
			file := schema.FileFS(yamltest.MapFS(map[string]string{
				"root.json": `{"properties": {"a": {"$ref": "http://schemas.test/defs.json#/$defs/a"}}}`,
			}), "root.json")
			reg := schema.NewRegistry(schema.WithHTTPClient(h.client), schema.WithRefreshAfter(time.Hour))

			// The URL schema loads /defs.json, and the file schema takes it
			// from the cache half an hour later.
			loadAll(t, reg, schema.URL(rootURL))
			time.Sleep(30 * time.Minute)

			first, err := reg.Schema(t.Context(), file)
			require.NoError(t, err)
			assert.Equal(t, map[string]int{"/root.json": 1, "/defs.json": 1}, h.requests())

			h.files.Store(&map[string]string{"/root.json": rootSchema, "/defs.json": defsString})
			time.Sleep(31 * time.Minute)

			// /defs.json fell due an hour after the URL schema loaded it,
			// and the file schema fell due with it.
			again, err := reg.Schema(t.Context(), file)
			require.NoError(t, err)
			assert.NotSame(t, first, again)
			require.ErrorContains(t, yamltest.FirstDocument(t, "a: 1\n").Validate(t.Context(), again), "string")
			assert.Equal(t, map[string]int{"/root.json": 1, "/defs.json": 2}, h.requests())
		})
	})

	t.Run("a schema falls due with a URL document a validation loaded", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// The host does not serve /defs.json until the schema compiled,
			// and each fetch takes ten seconds.
			const fetch = 10 * time.Second

			h := newHost(map[string]string{})
			client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
				time.Sleep(fetch)

				return h.client.Transport.RoundTrip(r)
			}).Client()
			file := schema.FileFS(yamltest.MapFS(map[string]string{
				"root.json": `{"properties": {"a": {"$ref": "http://schemas.test/defs.json#/$defs/a"}}}`,
			}), "root.json")
			reg := schema.NewRegistry(
				schema.WithHTTPClient(client),
				schema.WithRefreshAfter(time.Hour),
				schema.WithRetryAfter(time.Nanosecond),
				requireRefs(false),
			)

			first, err := reg.Schema(t.Context(), file)
			require.NoError(t, err)

			// The registry drops the failure of /defs.json after a
			// nanosecond, so the validation loads it.
			h.files.Store(&map[string]string{"/defs.json": defsInteger})
			time.Sleep(time.Nanosecond)
			require.NoError(t, yamltest.FirstDocument(t, "a: 1\n").Validate(t.Context(), first))
			assert.Equal(t, map[string]int{"/defs.json": 2}, h.requests())

			// The interval started when the load started, so it has a
			// nanosecond left.
			time.Sleep(time.Hour - fetch - time.Nanosecond)

			again, err := reg.Schema(t.Context(), file)
			require.NoError(t, err)
			require.Same(t, first, again)

			time.Sleep(time.Nanosecond)

			again, err = reg.Schema(t.Context(), file)
			require.NoError(t, err)
			assert.NotSame(t, first, again)
		})
	})

	t.Run("serves the held copy while the host does not answer", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			h := newHost(map[string]string{"/root.json": rootSchema, "/defs.json": defsInteger})

			// While the host hangs, the client waits for each request until
			// its Timeout or the context of the request ends. The request
			// then fails a second later. So a caller whose deadline ends
			// the request it started stops waiting before the load fails.
			var hung atomic.Bool

			client := &http.Client{
				Timeout: 30 * time.Second,
				Transport: yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if hung.Load() {
						<-r.Context().Done()
						time.Sleep(time.Second)

						return nil, r.Context().Err()
					}

					return h.client.Transport.RoundTrip(r)
				}),
			}

			ref := schema.URL(rootURL)
			file := schema.FileFS(yamltest.MapFS(map[string]string{
				"root.json": `{"properties": {"a": {"$ref": "http://schemas.test/defs.json#/$defs/a"}}}`,
			}), "root.json")
			reg := schema.NewRegistry(
				schema.WithHTTPClient(client),
				schema.WithResolvers(ref),
				schema.WithRefreshAfter(time.Hour),
				schema.WithRetryAfter(time.Minute),
			)

			held, err := reg.Schema(t.Context(), ref)
			require.NoError(t, err)

			hung.Store(true)
			time.Sleep(time.Hour)

			// The function returns what reg returns for ref to a caller
			// that waits for d at most, and how long the caller waited.
			schemaWithin := func(ref schema.Ref, d time.Duration) (*schema.Schema, time.Duration, error) {
				ctx, cancel := context.WithTimeout(t.Context(), d)
				defer cancel()

				start := time.Now()
				s, err := reg.Schema(ctx, ref)

				return s, time.Since(start), err
			}

			// The first request starts a refresh, waits for it until its
			// own deadline, and gets the held copy.
			got, waited, err := schemaWithin(ref, 5*time.Second)
			require.NoError(t, err)
			assert.Same(t, held, got)
			assert.Equal(t, 5*time.Second, waited)

			// A request that arrives while the refresh runs does not wait.
			got, waited, err = schemaWithin(ref, time.Hour)
			require.NoError(t, err)
			assert.Same(t, held, got)
			assert.Zero(t, waited)

			// The registry does not hold a schema for the file, so the
			// first request for it fails when its deadline ends. The
			// compile that it started reaches the held copy of /defs.json
			// and keeps the schema, which falls due with that copy. So the
			// next request starts a refresh and gets that schema.
			_, waited, err = schemaWithin(file, 5*time.Second)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, 5*time.Second, waited)

			// Let the compile finish. Its fetch of /defs.json fails a
			// second after the deadline, and the compile goes on with the
			// held copy.
			time.Sleep(time.Second)
			synctest.Wait()

			got, waited, err = schemaWithin(file, 5*time.Second)
			require.NoError(t, err)
			require.ErrorContains(t, yamltest.FirstDocument(t, "a: x\n").Validate(t.Context(), got), "integer")
			assert.Equal(t, 5*time.Second, waited)

			// The client ends the refresh of ref at its Timeout, and the
			// refresh fails a second later. The failure dates the held copy
			// again for the retry interval, so the requests inside it do
			// not start a refresh.
			time.Sleep(30 * time.Second)

			for range 5 {
				got, waited, err = schemaWithin(ref, time.Hour)
				require.NoError(t, err)
				assert.Same(t, held, got)
				assert.Zero(t, waited)
			}

			requireAccepts(t, reg, "a: 1\n")

			// The host comes back with a new version of the document, and
			// the first request after the retry interval fetches it.
			hung.Store(false)
			h.files.Store(&map[string]string{"/root.json": rootSchema, "/defs.json": defsString})
			time.Sleep(time.Minute)

			requireRejects(t, reg, "a: 1\n", "string")
		})
	})

	t.Run("serves the held copy when a release runs during a failed refresh", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The path whose fetch the host holds and then fails.
			held string
			// Whether the request that starts the refresh gets the schema
			// that the registry held. A refresh whose $ref fetch fails
			// compiles again with the held copy of the document instead.
			same bool
		}{
			"schema":                {held: "/root.json", same: true},
			"document a $ref names": {held: "/defs.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					h := newHost(map[string]string{"/root.json": rootSchema, "/defs.json": defsInteger})

					var down atomic.Bool

					gate := make(chan struct{})

					// While the host is down, the transport holds each fetch
					// of the path until gate closes, and then fails it.
					client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
						if down.Load() && r.URL.Path == tc.held {
							<-gate

							return nil, errors.New("host down")
						}

						return h.client.Transport.RoundTrip(r)
					}).Client()

					ref := schema.URL(rootURL)
					reg := schema.NewRegistry(
						schema.WithHTTPClient(client),
						schema.WithResolvers(ref),
						schema.WithRefreshAfter(time.Hour),
						schema.WithRetryAfter(time.Minute),
					)

					held, err := reg.Schema(t.Context(), ref)
					require.NoError(t, err)

					other := loading("other", []byte(`{"type": "object"}`), nil)
					_, err = reg.Schema(t.Context(), other)
					require.NoError(t, err)

					down.Store(true)
					time.Sleep(time.Hour)

					var (
						wg  sync.WaitGroup
						got *schema.Schema
					)

					wg.Go(func() {
						got, err = reg.Schema(t.Context(), ref)
					})

					// The refresh waits on gate. The program drops another
					// schema that the registry keeps, which starts a new
					// generation of the cache.
					synctest.Wait()
					reg.Invalidate(other)

					// A request after the release gets the held copy without
					// waiting for the refresh.
					ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
					defer cancel()

					start := time.Now()
					again, againErr := reg.Schema(ctx, ref)
					require.NoError(t, againErr)
					assert.Same(t, held, again)
					assert.Zero(t, time.Since(start))

					close(gate)
					wg.Wait()

					require.NoError(t, err)
					require.NoError(t, yamltest.FirstDocument(t, "a: 1\n").Validate(t.Context(), got))

					if tc.same {
						assert.Same(t, held, got)
					} else {
						assert.NotSame(t, held, got)
					}

					requireAccepts(t, reg, "a: 1\n")
				})
			})
		}
	})

	t.Run("does not reload a Loadable ref", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ref, loads := countingLoader(rootURL, []byte(`{"type": "object"}`))
			reg := schema.NewRegistry(schema.WithRefreshAfter(time.Hour))

			loadAll(t, reg, ref)
			time.Sleep(2 * time.Hour)
			loadAll(t, reg, ref)

			assert.Equal(t, int32(1), loads.Load())
		})
	})
}

func TestRegistry_FileFS(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "required": ["kind"]}`)

	bundle := yamltest.MapFS(map[string]string{
		"configs/app.yaml":        yamltest.SchemaDirective("./app.schema.json", "kind: App\n"),
		"configs/app.schema.json": string(schemaData),
		"schemas/pod.json":        string(schemaData),
	})

	// The working directory holds none of these files, so a read that
	// went to it would fail.
	wd, err := os.Getwd()
	require.NoError(t, err)

	t.Run("a file ref reads from the file system", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.FileFS(bundle, "schemas/pod.json")))

		data, err := reg.Load(t.Context(), schema.FileFS(bundle, "./schemas/pod.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)

		requireAccepts(t, reg, "kind: Pod\n")

		requireRejects(t, reg, "name: x\n", `missing required property "kind"`)
	})

	t.Run("a directive resolves beside its document", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
		require.NoError(t, err)

		doc := yamltest.Document(t, source)

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

		require.NoError(t, reg.Check(t.Context(), doc))

		// The same text with its relative path alone gives the directive
		// no directory to resolve beside.
		text := string(bundle["configs/app.yaml"].Data)
		relative := yamltest.FirstDocumentWithPath(t, text, "configs/app.yaml")

		err = reg.Check(t.Context(), relative)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorIs(t, err, schema.ErrNoBaseDir)

		// With an absolute path, it names a file on disk, where the schema
		// does not exist.
		onDisk := yamltest.FirstDocumentWithPath(t, text, filepath.Join(t.TempDir(), "configs", "app.yaml"))

		err = reg.Check(t.Context(), onDisk)
		requireLoadError(t, err, os.ErrNotExist)
	})

	t.Run("a directive with a fragment selects a subschema", func(t *testing.T) {
		t.Parallel()

		fsys := yamltest.MapFS(map[string]string{
			"configs/defs.json": `{"$defs": {"Foo": {"$ref": "#/$defs/Name"}, "Name": {"required": ["name"]}}}`,
		})

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

		// Each document names a subschema of defs.json by its pointer.
		app := func(pointer, body string) *niceyaml.Node {
			return yamltest.FirstDocument(t, yamltest.SchemaDirective("./defs.json#"+pointer, body),
				niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(fsys))
		}

		valid := app("/$defs/Foo", "name: x\n")
		require.NoError(t, reg.Check(t.Context(), valid))

		invalid := app("/$defs/Foo", "other: 1\n")
		err := reg.Check(t.Context(), invalid)
		require.ErrorContains(t, err, `missing required property "name"`)

		// A pointer that names nothing reports the path in the file system.
		missing := app("/$defs/Missing", "name: x\n")
		err = reg.Check(t.Context(), missing)
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.Contains(t, err.Error(), `"configs/defs.json#/$defs/Missing"`)
		assert.NotContains(t, err.Error(), wd)
	})

	t.Run("a $ref through the parent stays in the file system", func(t *testing.T) {
		t.Parallel()

		fsys := yamltest.MapFS(map[string]string{
			"schemas/app/main.json": `{"properties": {"a": {"$ref": "../defs/a.json"}}}`,
			"schemas/defs/a.json":   `{"type": "string"}`,
		})

		reg := schema.NewRegistry(schema.WithResolvers(schema.FileFS(fsys, "schemas/app/main.json")))

		requireAccepts(t, reg, "a: x\n")

		requireRejects(t, reg, "a: 5\n", `$.a: expected "string", got "integer"`)
	})

	t.Run("a missing file reports the path in the file system", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		_, err := reg.Load(t.Context(), schema.FileFS(bundle, "./schemas/missing.json"))
		requireLoadError(t, err, fs.ErrNotExist)
		assert.Equal(t,
			`load schema: "./schemas/missing.json": read schemas/missing.json: `+
				`open schemas/missing.json: file does not exist`,
			err.Error())
	})

	t.Run("a missing $ref reports the path in the file system", func(t *testing.T) {
		t.Parallel()

		fsys := yamltest.MapFS(map[string]string{
			"schemas/root.json": `{"properties": {"a": {"$ref": "defs.json"}}}`,
		})

		for name, mode := range refModes {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(
					requireRefs(mode.require),
					schema.WithResolvers(schema.FileFS(fsys, "schemas/root.json")),
				)

				err := checkInput(t, reg, "a: 1\n")
				require.ErrorIs(t, err, mode.err)
				assert.Contains(t, err.Error(), "read schemas/defs.json: open schemas/defs.json: file does not exist")
				assert.NotContains(t, err.Error(), wd)
			})
		}
	})

	t.Run("a path outside the file system names no file", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			path string
		}{
			"absolute path under the working directory": {path: filepath.Join(wd, "schemas", "pod.json")},
			"absolute path elsewhere":                   {path: filepath.Join(t.TempDir(), "pod.json")},
			"rooted path":                               {path: "/schemas/pod.json"},
			"drive-letter path":                         {path: "C:/schemas/pod.json"},
			"relative path through the parent":          {path: "../schemas/pod.json"},
			"relative path out of the root and back":    {path: "../" + filepath.Base(wd) + "/schemas/pod.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry()

				_, err := reg.Load(t.Context(), schema.FileFS(bundle, tc.path))
				requireLoadError(t, err, schema.ErrFileNotAllowed)
				require.NotErrorIs(t, err, fs.ErrInvalid)

				_, err = reg.Schema(t.Context(), schema.FileFS(bundle, tc.path))
				requireLoadError(t, err, schema.ErrFileNotAllowed)
				assert.False(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("a directive in a document opened by absolute path names no file", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t,
			yamltest.SchemaDirective("./app.schema.json", "kind: App\n"),
			niceyaml.WithFilePath(filepath.Join(wd, "configs", "app.yaml")), niceyaml.WithFS(bundle))

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

		err := reg.Check(t.Context(), doc)
		requireLoadError(t, err, schema.ErrFileNotAllowed)
	})

	t.Run("a file and a loadable with one name stay apart", func(t *testing.T) {
		t.Parallel()

		fsys := yamltest.MapFS(map[string]string{"s.json": `{"type": "string"}`})

		file := schema.FileFS(fsys, "s.json")
		loadable := loading("s.json", []byte(`{"type": "integer"}`), nil)

		assert.NotEqual(t, loadable.Key(), file.Key())

		// Load the two in both orders, each in a registry of its own.
		for _, loadableFirst := range []bool{true, false} {
			reg := schema.NewRegistry()

			if loadableFirst {
				_, err := reg.Schema(t.Context(), loadable)
				require.NoError(t, err)
			}

			fromFile, err := reg.Schema(t.Context(), file)
			require.NoError(t, err)

			fromLoadable, err := reg.Schema(t.Context(), loadable)
			require.NoError(t, err)

			require.NoError(t, fromFile.ValidateValue(t.Context(), "x"))
			require.Error(t, fromFile.ValidateValue(t.Context(), 5))

			require.NoError(t, fromLoadable.ValidateValue(t.Context(), 5))
			require.Error(t, fromLoadable.ValidateValue(t.Context(), "x"))
		}
	})

	t.Run("a named pipe is invalid", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{
			"schemas/pipe.json": &fstest.MapFile{Mode: fs.ModeNamedPipe},
		}

		reg := schema.NewRegistry()

		_, err := reg.Load(t.Context(), schema.FileFS(fsys, "schemas/pipe.json"))
		requireLoadError(t, err, fs.ErrInvalid)
	})

	t.Run("an oversize file exceeds the limit", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{
			"schemas/big.json": &fstest.MapFile{Data: make([]byte, schema.DefaultLoadLimit+1)},
		}

		reg := schema.NewRegistry()

		_, err := reg.Load(t.Context(), schema.FileFS(fsys, "schemas/big.json"))
		requireLoadError(t, err, niceyaml.ErrLimit)
		require.ErrorContains(t, err, "read schemas/big.json: file is larger than 10 MiB")
	})

	t.Run("an os.Root refuses a symlink out of its tree", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		target := yamltest.WriteFile(t, t.TempDir(), "s.json", string(schemaData))
		yamltest.SymlinkOrSkip(t, target, filepath.Join(root, "s.json"))

		reg := schema.NewRegistry()

		_, err := reg.Load(t.Context(), schema.FileFS(rootFS(t, root), "s.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.NotErrorIs(t, err, schema.ErrFileNotAllowed)

		// The file system of os.DirFS follows the link and reads the file
		// outside the tree.
		data, err := reg.Load(t.Context(), schema.FileFS(os.DirFS(root), "s.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("a nil file system and an empty path panic", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.FileFS: fsys is nil", func() {
			schema.FileFS(nil, "schemas/pod.json")
		})
		assert.Panics(t, func() {
			schema.FileFS(bundle, "")
		})
	})

	t.Run("the key is the URL of the path in the file system", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "file:///schemas/pod.json", schema.FileFS(bundle, "./schemas/../schemas/pod.json").Key())
		assert.NotContains(t, schema.FileFS(bundle, "schemas/pod.json").Key(), filepath.ToSlash(wd))
	})

	t.Run("a file system ref reads past an access that reads no file", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(withAccess(t))

		data, err := reg.Load(t.Context(), schema.FileFS(bundle, "schemas/pod.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)

		_, err = reg.Load(t.Context(), schema.File(filepath.Join(wd, "schemas", "pod.json")))
		requireLoadError(t, err, schema.ErrFileNotAllowed)
	})
}

// One registry serves documents and schemas of several file systems and
// of the disk. Each directive resolves beside its document, in the file
// system the document came from.
func TestRegistry_FileSystems(t *testing.T) {
	t.Parallel()

	const refsDefs = `{"properties": {"v": {"$ref": "defs.json"}}}`

	// Both file systems and the directory on disk hold the same paths and
	// the same schema. The documents its $ref names differ.
	stringFS := yamltest.MapFS(map[string]string{
		"configs/app.yaml":    yamltest.SchemaDirective("./schema.json", "v: x\n"),
		"configs/schema.json": refsDefs,
		"configs/defs.json":   `{"type": "string"}`,
	})
	integerFS := yamltest.MapFS(map[string]string{
		"configs/app.yaml":    yamltest.SchemaDirective("./schema.json", "v: 5\n"),
		"configs/schema.json": refsDefs,
		"configs/defs.json":   `{"type": "integer"}`,
	})

	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, map[string]string{
		"configs/app.yaml":    yamltest.SchemaDirective("./schema.json", "v: true\n"),
		"configs/schema.json": refsDefs,
		"configs/defs.json":   `{"type": "boolean"}`,
	})

	reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

	open := map[string]func() (*niceyaml.Source, error){
		"strings": func() (*niceyaml.Source, error) {
			return niceyaml.NewSourceFromFS(stringFS, "configs/app.yaml")
		},
		"integers": func() (*niceyaml.Source, error) {
			return niceyaml.NewSourceFromFS(integerFS, "configs/app.yaml")
		},
		"disk": func() (*niceyaml.Source, error) {
			return niceyaml.NewSourceFromFile(filepath.Join(dir, "configs", "app.yaml"))
		},
	}

	// Each document passes the schema beside it and fails the other two,
	// in every order the registry meets them.
	for _, order := range [][]string{
		{"strings", "integers", "disk"},
		{"disk", "integers", "strings"},
		{"integers", "disk", "strings"},
	} {
		for _, name := range order {
			source, err := open[name]()
			require.NoError(t, err)
			require.NoError(t, source.ValidateDocuments(t.Context(), reg), "%s in %v", name, order)
		}
	}

	t.Run("a document fails the schema of its own file system", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, yamltest.SchemaDirective("./schema.json", "v: 5\n"),
			niceyaml.WithFilePath("configs/other.yaml"), niceyaml.WithFS(stringFS))

		err := reg.Check(t.Context(), doc)
		require.ErrorContains(t, err, `$.v: expected "string", got "integer"`)
	})

	t.Run("one path in two file systems is two schemas", func(t *testing.T) {
		t.Parallel()

		fromStrings, err := reg.Schema(t.Context(), schema.FileFS(stringFS, "configs/schema.json"))
		require.NoError(t, err)

		fromIntegers, err := reg.Schema(t.Context(), schema.FileFS(integerFS, "configs/schema.json"))
		require.NoError(t, err)

		assert.NotSame(t, fromStrings, fromIntegers)

		again, err := reg.Schema(t.Context(), schema.FileFS(stringFS, "./configs/schema.json"))
		require.NoError(t, err)
		assert.Same(t, fromStrings, again)

		require.NoError(t, fromStrings.ValidateValue(t.Context(), map[string]any{"v": "x"}))
		require.Error(t, fromStrings.ValidateValue(t.Context(), map[string]any{"v": 5}))
		require.NoError(t, fromIntegers.ValidateValue(t.Context(), map[string]any{"v": 5}))
		require.Error(t, fromIntegers.ValidateValue(t.Context(), map[string]any{"v": "x"}))
	})
}

// The registry tells one file system from another by its value, so the
// value must be one Go can compare, or a map.
func TestRegistry_FileSystemIdentity(t *testing.T) {
	t.Parallel()

	files := yamltest.MapFS(map[string]string{"s.json": `{"type": "string"}`})

	t.Run("a value Go cannot compare names no file system", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		_, err := reg.Schema(t.Context(), schema.FileFS(sliceFS{files: files}, "s.json"))
		requireLoadError(t, err, fs.ErrInvalid)
		require.ErrorContains(t, err, "pass a pointer to it")
	})

	// Another pointer or another map is another file system, whatever it
	// holds.
	tcs := map[string]struct {
		fsys  fs.FS
		other fs.FS
	}{
		"a pointer to it does": {
			fsys:  &sliceFS{files: files},
			other: &sliceFS{files: files},
		},
		"a map is known by the map itself": {
			fsys:  files,
			other: maps.Clone(files),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry()

			first, err := reg.Schema(t.Context(), schema.FileFS(tc.fsys, "s.json"))
			require.NoError(t, err)

			second, err := reg.Schema(t.Context(), schema.FileFS(tc.fsys, "s.json"))
			require.NoError(t, err)
			assert.Same(t, first, second)

			other, err := reg.Schema(t.Context(), schema.FileFS(tc.other, "s.json"))
			require.NoError(t, err)
			assert.NotSame(t, first, other)
		})
	}

	t.Run("a map at the address of a collected one loads its own files", func(t *testing.T) {
		t.Parallel()

		// Each pair of maps holds different files from the pair before it.
		// The collector runs after each map, so a map can take the address
		// of a collected map that held other files. Each step passes only
		// when the registry reads each map's schemas from that map's own
		// files.
		tcs := map[string]struct {
			step func(t *testing.T, reg *schema.Registry, i int, odd bool)
			opts []schema.RegistryOption
		}{
			"remembered failure": {
				step: func(t *testing.T, reg *schema.Registry, _ int, odd bool) {
					t.Helper()

					// The map of an odd pair holds no schema, and the
					// registry remembers that failure.
					files := map[string]string{"s.json": `{"type": "string"}`}
					if odd {
						files = nil
					}

					_, err := reg.Schema(t.Context(), schema.FileFS(yamltest.MapFS(files), "s.json"))
					if odd {
						requireLoadError(t, err, fs.ErrNotExist)
					} else {
						require.NoError(t, err)
					}
				},
			},
			"document a $ref names": {
				opts: []schema.RegistryOption{schema.WithCacheLimit(1)},
				step: func(t *testing.T, reg *schema.Registry, i int, odd bool) {
					t.Helper()

					typ, value := "string", any("x")
					if odd {
						typ, value = "integer", 5
					}

					fsys := yamltest.MapFS(map[string]string{
						"s.json":    `{"$ref": "defs.json"}`,
						"defs.json": fmt.Sprintf(`{"type": %q}`, typ),
					})

					s, err := reg.Schema(t.Context(), schema.FileFS(fsys, "s.json"))
					require.NoError(t, err)
					require.NoError(t, s.ValidateValue(t.Context(), value))

					// A schema from no file system takes the place of the
					// schema, and the document its $ref names stays.
					loadAll(t, reg, loading(fmt.Sprintf("other-%d", i), []byte(`{}`), nil))
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(tc.opts...)

				for i := range 40 {
					tc.step(t, reg, i, i/2%2 == 1)
					runtime.GC()
				}
			})
		}
	})
}

// sliceFS is a file system whose value Go cannot compare, since it holds
// a map by value.
type sliceFS struct {
	files fstest.MapFS
}

func (f sliceFS) Open(name string) (fs.File, error) {
	return f.files.Open(name) //nolint:wrapcheck // The test file system returns the error of the one it wraps.
}

// A heldFS is a file system that a test watches through a weak pointer,
// to see whether a registry still holds it.
type heldFS struct {
	// The file system, until the test drops its own hold on it.
	fsys fs.FS
	// A file that only the map holds, so it lives exactly as long as the
	// map does. The registry never reads it.
	file weak.Pointer[fstest.MapFile]
}

// newHeldFS creates a new [heldFS] whose file system is a map that
// holds files, or the file system wrap returns for that map. A nil wrap
// keeps the map.
func newHeldFS(files map[string]string, wrap func(fstest.MapFS) fs.FS) *heldFS {
	files = maps.Clone(files)
	if files == nil {
		files = map[string]string{}
	}

	files["held.txt"] = "held"

	fsys := yamltest.MapFS(files)
	h := &heldFS{fsys: fsys, file: weak.Make(fsys["held.txt"])}

	if wrap != nil {
		h.fsys = wrap(fsys)
	}

	return h
}

// held drops the hold of the test on the file system, runs the
// collector, and reports whether anything else still holds the file
// system. A goroutine of the registry might hold the file system for a
// moment after its result reaches the caller, until the goroutine exits.
// To wait for such a goroutine, held runs the collector up to ten times
// and yields to other goroutines between runs. It reports a hold only
// when every run leaves the file system alive.
func (h *heldFS) held() bool {
	h.fsys = nil

	for range 10 {
		runtime.GC()

		if h.file.Value() == nil {
			return false
		}

		runtime.Gosched()
	}

	return true
}

// asPointer returns a pointer file system that reads from files.
func asPointer(files fstest.MapFS) fs.FS {
	return &sliceFS{files: files}
}

func TestRegistry_Forget(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"configs/app.yaml":    yamltest.SchemaDirective("../schemas/s.json", "a: x\n"),
		"schemas/s.json":      `{"properties": {"a": {"$ref": "defs.json"}}}`,
		"schemas/defs.json":   `{"type": "string"}`,
		"schemas/broken.json": `{"properties": {"a": {"$ref": "missing.json"}}}`,
	}

	t.Run("stops holding the file system", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			wrap func(fstest.MapFS) fs.FS
			// Asks the registry for a schema in fsys.
			load func(ctx context.Context, reg *schema.Registry, fsys fs.FS) error
			err  error
		}{
			"schema and document a $ref names": {
				load: loadFS("schemas/s.json"),
			},
			"file system known by a pointer": {
				wrap: asPointer,
				load: loadFS("schemas/s.json"),
			},
			"schema that does not load": {
				load: loadFS("schemas/missing.json"),
				err:  fs.ErrNotExist,
			},
			"document a $ref names that does not load": {
				load: loadFS("schemas/broken.json"),
				err:  fs.ErrNotExist,
			},
			"directive beside a document": {
				load: func(ctx context.Context, reg *schema.Registry, fsys fs.FS) error {
					source, err := niceyaml.NewSourceFromFS(fsys, "configs/app.yaml")
					if err != nil {
						return err
					}

					return source.ValidateDocuments(ctx, reg) //nolint:wrapcheck // The test inspects the error itself.
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(
					schema.WithAccess(schema.Unrestricted()),
					schema.WithResolvers(schema.Directive()),
				)
				h := newHeldFS(files, tc.wrap)

				err := tc.load(t.Context(), reg, h.fsys)
				if tc.err != nil {
					requireLoadError(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}

				reg.Forget(h.fsys)
				assert.False(t, h.held())

				runtime.KeepAlive(reg)
			})
		}
	})

	t.Run("loads the schema again", func(t *testing.T) {
		t.Parallel()

		fsys := &openCountingFS{files: yamltest.MapFS(files)}
		reg := schema.NewRegistry()

		first, err := reg.Schema(t.Context(), schema.FileFS(fsys, "schemas/s.json"))
		require.NoError(t, err)

		opens := fsys.opens.Load()

		reg.Forget(fsys)

		again, err := reg.Schema(t.Context(), schema.FileFS(fsys, "schemas/s.json"))
		require.NoError(t, err)
		assert.NotSame(t, first, again)

		// The schema and the document its $ref names load again.
		assert.Equal(t, 2*opens, fsys.opens.Load())
	})

	t.Run("keeps every other entry", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The file system to forget.
			fsys fs.FS
		}{
			"another file system": {
				fsys: yamltest.MapFS(files),
			},
			"nil file system": {},
			"file system Go cannot compare": {
				fsys: sliceFS{files: yamltest.MapFS(files)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				kept := yamltest.MapFS(files)
				loadable, loads := countingLoader("loadable", []byte(`{}`))

				reg := schema.NewRegistry()

				fromFS, err := reg.Schema(t.Context(), schema.FileFS(kept, "schemas/s.json"))
				require.NoError(t, err)

				fromLoadable, err := reg.Schema(t.Context(), loadable)
				require.NoError(t, err)

				reg.Forget(tc.fsys)

				again, err := reg.Schema(t.Context(), schema.FileFS(kept, "schemas/s.json"))
				require.NoError(t, err)
				assert.Same(t, fromFS, again)

				again, err = reg.Schema(t.Context(), loadable)
				require.NoError(t, err)
				assert.Same(t, fromLoadable, again)
				assert.Equal(t, int32(1), loads.Load())
			})
		}
	})

	t.Run("keeps the files it read through AllowRoot", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		yamltest.WriteFiles(t, dir, files)

		root := openRoot(t, dir)
		ref := schema.File(filepath.Join(dir, "schemas", "s.json"))

		reg := schema.NewRegistry(withAccess(t, schema.AllowRoot(root)))

		first, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		reg.Forget(root.FS())

		again, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.Same(t, first, again)
	})
}

// loadFS returns a function that asks a registry for the schema at path
// in a file system.
func loadFS(path string) func(context.Context, *schema.Registry, fs.FS) error {
	return func(ctx context.Context, reg *schema.Registry, fsys fs.FS) error {
		_, err := reg.Schema(ctx, schema.FileFS(fsys, path))

		return err //nolint:wrapcheck // The test inspects the error itself.
	}
}

// The schemas and the documents the tests of the release methods load.
const (
	// A schema whose member a takes a string.
	stringA = `{"properties": {"a": {"type": "string"}}}`
	// A schema whose member a takes an integer.
	integerA = `{"properties": {"a": {"type": "integer"}}}`
	// A schema whose member a takes what defs.json beside it takes.
	refA = `{"properties": {"a": {"$ref": "defs.json"}}}`
	// A document that takes a string.
	stringDefs = `{"type": "string"}`
	// A document that takes an integer.
	integerDefs = `{"type": "integer"}`
	// The violation of the document "a: x" against a schema whose
	// member a takes an integer.
	notInteger = `expected "integer", got "string"`
)

// requireCheck asks reg for the schema ref names and validates the
// document "a: x" with it. The request must pass. For a non-empty want,
// the error of the validation must contain want. For an empty want, the
// validation must pass.
func requireCheck(t *testing.T, reg *schema.Registry, ref schema.Ref, want string) {
	t.Helper()

	s, err := reg.Schema(t.Context(), ref)
	require.NoError(t, err)

	err = yamltest.FirstDocument(t, "a: x\n").Validate(t.Context(), s)
	if want == "" {
		require.NoError(t, err)

		return
	}

	require.ErrorContains(t, err, want)
}

// fileRef returns a Ref for the file name in dir. A '#' in name opens
// the fragment of the Ref.
func fileRef(t *testing.T, dir, name string) schema.Ref {
	t.Helper()

	base, fragment, ok := strings.Cut(name, "#")
	if !ok {
		return schema.File(filepath.Join(dir, name))
	}

	return fileOrURL(t, dir, plainFileURL(filepath.Join(dir, base))+"#"+fragment)
}

// A gatedFS is a file system that holds one open of one file until its
// gate closes, and counts the opens of that file. It holds the first open
// that follows the skipped ones. It has no method but Open, so every read
// goes through the count.
type gatedFS struct {
	files   fstest.MapFS
	started chan struct{} // gets a value when the held open of name starts to wait
	gate    <-chan struct{}
	name    string
	opens   atomic.Int32
	skip    int32 // the opens of name that pass before the held one
}

func (f *gatedFS) Open(name string) (fs.File, error) {
	if name == f.name && f.opens.Add(1) == f.skip+1 {
		f.started <- struct{}{}

		<-f.gate
	}

	return f.files.Open(name) //nolint:wrapcheck // The test file system returns the error of the one it wraps.
}

func TestRegistry_Invalidate(t *testing.T) {
	t.Parallel()

	t.Run("loads the changed document again", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The error the request for the schema returns before
			// Invalidate, or nil for a check that passes.
			stale error
			// The files in the directory before the change. In each
			// file, "{dir}" stands for the URL path of the directory.
			before map[string]string
			// The files the change writes.
			after map[string]string
			// The schemas a request loads before the change.
			warm []string
			// The schema a request loads after the change.
			ref string
			// The file the program passes to Invalidate.
			changed string
			// Text the error of the check holds after Invalidate, or ""
			// for a check that passes.
			want string
		}{
			"schema file": {
				before:  map[string]string{"s.json": stringA},
				after:   map[string]string{"s.json": integerA},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "s.json",
				want:    notInteger,
			},
			"fragment of the schema file": {
				before:  map[string]string{"s.json": `{"$defs": {"obj": ` + stringA + `}}`},
				after:   map[string]string{"s.json": `{"$defs": {"obj": ` + integerA + `}}`},
				warm:    []string{"s.json#/$defs/obj"},
				ref:     "s.json#/$defs/obj",
				changed: "s.json",
				want:    notInteger,
			},
			"document a $ref names": {
				before:  map[string]string{"s.json": refA, "defs.json": stringDefs},
				after:   map[string]string{"defs.json": integerDefs},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "defs.json",
				want:    notInteger,
			},
			"document a $ref names with a query": {
				before: map[string]string{
					"s.json":    `{"properties": {"a": {"$ref": "defs.json?v=1"}}}`,
					"defs.json": stringDefs,
				},
				after:   map[string]string{"defs.json": integerDefs},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "defs.json",
				want:    notInteger,
			},
			"document a $ref names by a localhost URL": {
				before: map[string]string{
					"s.json":    `{"properties": {"a": {"$ref": "file://localhost{dir}/defs.json"}}}`,
					"defs.json": stringDefs,
				},
				after:   map[string]string{"defs.json": integerDefs},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "defs.json",
				want:    notInteger,
			},
			"document a $ref names through another document": {
				before: map[string]string{
					"s.json":    `{"properties": {"a": {"$ref": "mid.json"}}}`,
					"mid.json":  `{"$ref": "defs.json"}`,
					"defs.json": stringDefs,
				},
				after:   map[string]string{"defs.json": integerDefs},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "defs.json",
				want:    notInteger,
			},
			// The registry keeps defs.json for every schema that names it.
			// Without Invalidate, a schema it compiles after the change
			// reads the document another schema loaded before the change.
			"document another schema loaded": {
				before:  map[string]string{"s.json": refA, "t.json": refA, "defs.json": stringDefs},
				after:   map[string]string{"defs.json": integerDefs},
				warm:    []string{"s.json"},
				ref:     "t.json",
				changed: "defs.json",
				want:    notInteger,
			},
			"schema file that did not exist": {
				after:   map[string]string{"s.json": stringA},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "s.json",
				stale:   fs.ErrNotExist,
			},
			"document a $ref names that did not exist": {
				before:  map[string]string{"s.json": refA},
				after:   map[string]string{"defs.json": stringDefs},
				warm:    []string{"s.json"},
				ref:     "s.json",
				changed: "defs.json",
				stale:   fs.ErrNotExist,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				dirPath := "/" + strings.TrimPrefix(filepath.ToSlash(dir), "/")

				before := make(map[string]string, len(tc.before))
				for name, data := range tc.before {
					before[name] = strings.ReplaceAll(data, "{dir}", dirPath)
				}

				yamltest.WriteFiles(t, dir, before)

				reg := schema.NewRegistry()

				check := func(ref schema.Ref) {
					t.Helper()

					if tc.stale == nil {
						requireCheck(t, reg, ref, "")

						return
					}

					_, err := reg.Schema(t.Context(), ref)
					requireLoadError(t, err, tc.stale)
				}

				for _, warm := range tc.warm {
					check(fileRef(t, dir, warm))
				}

				yamltest.WriteFiles(t, dir, tc.after)

				ref := fileRef(t, dir, tc.ref)
				check(ref)

				reg.Invalidate(fileRef(t, dir, tc.changed))
				requireCheck(t, reg, ref, tc.want)
			})
		}
	})

	t.Run("keeps every other entry", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		yamltest.WriteFiles(t, dir, map[string]string{
			"s.json":     refA,
			"defs.json":  stringDefs,
			"other.json": stringA,
		})

		files := map[string]string{"s.json": refA, "defs.json": stringDefs}

		tcs := map[string]struct {
			// The Refs the program passes to Invalidate.
			refs []schema.Ref
		}{
			"no Ref": {},
			"another file": {
				refs: []schema.Ref{schema.File(filepath.Join(dir, "other.json"))},
			},
			"same path in another file system": {
				refs: []schema.Ref{schema.FileFS(yamltest.MapFS(files), "defs.json")},
			},
			"zero Ref": {
				refs: []schema.Ref{{}},
			},
			"compiled schema": {
				refs: []schema.Ref{schema.MustCompile([]byte(stringA)).Ref()},
			},
			"Ref that names no file": {
				refs: []schema.Ref{schema.FileFS(yamltest.MapFS(files), "../defs.json")},
			},
			// A Ref from URL with the key of a file does not name the
			// file, whether a request loaded the file or a $ref reached
			// it.
			"URL Ref with the key of a file": {
				refs: []schema.Ref{
					schema.URL(schema.File(filepath.Join(dir, "s.json")).Key()),
					schema.URL(schema.File(filepath.Join(dir, "defs.json")).Key()),
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				fsys := &openCountingFS{files: yamltest.MapFS(files)}
				loadable, loads := countingLoader("loadable", []byte(stringA))

				refs := []schema.Ref{
					schema.File(filepath.Join(dir, "s.json")),
					schema.FileFS(fsys, "s.json"),
					loadable,
				}

				reg := schema.NewRegistry()

				first := make([]*schema.Schema, len(refs))

				for i, ref := range refs {
					s, err := reg.Schema(t.Context(), ref)
					require.NoError(t, err)

					first[i] = s
				}

				opens := fsys.opens.Load()

				reg.Invalidate(tc.refs...)

				for i, ref := range refs {
					again, err := reg.Schema(t.Context(), ref)
					require.NoError(t, err)
					assert.Same(t, first[i], again)
				}

				assert.Equal(t, opens, fsys.opens.Load())
				assert.Equal(t, int32(1), loads.Load())
			})
		}
	})

	t.Run("load in flight", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		files := map[string]string{"defs.json": stringDefs}

		// Each registry keeps the schema of this Ref before the load.
		known := loading("known", []byte(stringA), nil)

		tcs := map[string]struct {
			// The Refs the program passes to Invalidate during the load of
			// another schema.
			refs []schema.Ref
			// Whether the program also passes the Ref of that load.
			gated bool
			// Whether the registry keeps the schema of that load.
			wantKept bool
		}{
			"no Ref": {
				wantKept: true,
			},
			"zero Ref": {
				refs:     []schema.Ref{{}},
				wantKept: true,
			},
			"compiled schema": {
				refs:     []schema.Ref{schema.MustCompile([]byte(stringA)).Ref()},
				wantKept: true,
			},
			"Ref that names no file": {
				refs:     []schema.Ref{schema.FileFS(yamltest.MapFS(files), "../defs.json")},
				wantKept: true,
			},
			"URL Ref with the key of a file": {
				refs:     []schema.Ref{schema.URL(schema.File(filepath.Join(dir, "defs.json")).Key())},
				wantKept: true,
			},
			// The registry did not read any of these documents, so the
			// load cannot hold an old copy of one.
			"file the registry did not read": {
				refs:     []schema.Ref{schema.File(filepath.Join(dir, "defs.json"))},
				wantKept: true,
			},
			"file in a file system the registry did not read": {
				refs:     []schema.Ref{schema.FileFS(yamltest.MapFS(files), "defs.json")},
				wantKept: true,
			},
			"URL the registry did not fetch": {
				refs:     []schema.Ref{schema.URL("http://schemas.test/defs.json")},
				wantKept: true,
			},
			"Ref from Loadable the registry did not load": {
				refs:     []schema.Ref{loading("other", []byte(stringA), nil)},
				wantKept: true,
			},
			"the Ref of the load": {
				gated: true,
			},
			"the Ref of the load among others": {
				refs:  []schema.Ref{schema.File(filepath.Join(dir, "defs.json"))},
				gated: true,
			},
			// A release of a schema that the registry keeps starts a new
			// generation, which discards every load in flight.
			"Ref of a schema the registry keeps": {
				refs: []schema.Ref{known},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				type result struct {
					err error
					s   *schema.Schema
				}

				var loads atomic.Int32

				gate := make(chan struct{})
				started := make(chan struct{}, 2)

				ref := schema.Loadable("gated", func(context.Context) ([]byte, error) {
					loads.Add(1)

					started <- struct{}{}

					<-gate

					return []byte(stringA), nil
				})

				reg := schema.NewRegistry()

				_, err := reg.Schema(t.Context(), known)
				require.NoError(t, err)

				during := make(chan result, 1)

				go func() {
					s, err := reg.Schema(t.Context(), ref)
					during <- result{s: s, err: err}
				}()

				<-started

				refs := tc.refs
				if tc.gated {
					refs = append(slices.Clone(refs), ref)
				}

				reg.Invalidate(refs...)

				close(gate)

				fromDuring := <-during
				require.NoError(t, fromDuring.err)

				again, err := reg.Schema(t.Context(), ref)
				require.NoError(t, err)

				// A call that names a document the registry knows keeps the
				// load out of the cache, so the next request loads the
				// schema again.
				if tc.wantKept {
					assert.Same(t, fromDuring.s, again)
					assert.Equal(t, int32(1), loads.Load())
				} else {
					assert.NotSame(t, fromDuring.s, again)
					assert.Equal(t, int32(2), loads.Load())
				}
			})
		}
	})

	t.Run("URL", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The URL of the schema a request loads.
			schemaURL string
			// The URL the program passes to Invalidate.
			changed string
			// The number of requests the server gets for each path by
			// the end of the test.
			want map[string]int
		}{
			"document a $ref names": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://schemas.test/defs.json",
				want:      map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			"host in upper case": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://SCHEMAS.TEST/defs.json",
				want:      map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			"path with dot segments": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://schemas.test/x/../defs.json",
				want:      map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			"fragment": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://schemas.test/defs.json#/$defs/x",
				want:      map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			"userinfo the schema carries": {
				schemaURL: "http://user:pw@schemas.test/s.json",
				changed:   "http://schemas.test/defs.json",
				want:      map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			"userinfo the program passes": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://user:pw@schemas.test/defs.json",
				want:      map[string]int{"/s.json": 2, "/defs.json": 2},
			},
			"schema URL": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://schemas.test/s.json",
				want:      map[string]int{"/s.json": 2, "/defs.json": 1},
			},
			"another URL": {
				schemaURL: "http://schemas.test/s.json",
				changed:   "http://schemas.test/other.json",
				want:      map[string]int{"/s.json": 1, "/defs.json": 1},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				handler, requests := countingHandler(schemaHandler(map[string]string{
					"/s.json":    refA,
					"/defs.json": stringDefs,
				}))

				reg := schema.NewRegistry(schema.WithHTTPClient(handlerTransport(handler).Client()))

				requireCheck(t, reg, schema.URL(tc.schemaURL), "")

				reg.Invalidate(schema.URL(tc.changed))

				requireCheck(t, reg, schema.URL(tc.schemaURL), "")
				assert.Equal(t, tc.want, requests())
			})
		}
	})

	t.Run("a loadable and a URL with one key", func(t *testing.T) {
		t.Parallel()

		// The registry fetches defs.json for its own Ref and again for the
		// $ref of s.json. The Ref from Loadable goes by the URL of
		// defs.json.
		const defsURL = "http://schemas.test/defs.json"

		tcs := map[string]struct {
			// Builds the Ref the program passes to Invalidate from the Ref
			// from Loadable.
			changed func(loadable schema.Ref) schema.Ref
			// The number of requests the server gets for each path by
			// the end of the test.
			want map[string]int
			// The number of times the load of the Ref from Loadable runs
			// by the end of the test.
			wantLoads int32
		}{
			"Invalidate for the URL keeps the loadable": {
				changed:   func(schema.Ref) schema.Ref { return schema.URL(defsURL) },
				want:      map[string]int{"/s.json": 2, "/defs.json": 4},
				wantLoads: 1,
			},
			"Invalidate for the loadable keeps the URL": {
				changed:   func(loadable schema.Ref) schema.Ref { return loadable },
				want:      map[string]int{"/s.json": 1, "/defs.json": 2},
				wantLoads: 2,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				handler, requests := countingHandler(schemaHandler(map[string]string{
					"/s.json":    refA,
					"/defs.json": stringDefs,
				}))
				loadable, loads := countingLoader(defsURL, []byte(integerDefs))

				refs := []schema.Ref{
					schema.URL("http://schemas.test/s.json"),
					schema.URL(defsURL),
					loadable,
				}

				reg := schema.NewRegistry(schema.WithHTTPClient(handlerTransport(handler).Client()))

				for _, ref := range refs {
					_, err := reg.Schema(t.Context(), ref)
					require.NoError(t, err)
				}

				reg.Invalidate(tc.changed(loadable))

				for _, ref := range refs {
					_, err := reg.Schema(t.Context(), ref)
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, requests())
				assert.Equal(t, tc.wantLoads, loads.Load())
			})
		}
	})

	t.Run("a loadable and a file with one key", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// Picks the Ref the program passes to Invalidate.
			changed func(file, loadable schema.Ref) schema.Ref
			// Whether the registry keeps the schema of the file.
			wantFileKept bool
			// The number of times the load of the Ref from Loadable runs
			// by the end of the test.
			wantLoads int32
		}{
			"Invalidate for the file keeps the loadable": {
				changed:   func(file, _ schema.Ref) schema.Ref { return file },
				wantLoads: 1,
			},
			"Invalidate for the loadable keeps the file": {
				changed:      func(_, loadable schema.Ref) schema.Ref { return loadable },
				wantFileKept: true,
				wantLoads:    2,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The Ref from Loadable goes by the file URL of s.json.
				file := schema.File(yamltest.WriteFile(t, t.TempDir(), "s.json", stringDefs))
				loadable, loads := countingLoader(file.Key(), []byte(integerDefs))

				reg := schema.NewRegistry()

				fromFile, err := reg.Schema(t.Context(), file)
				require.NoError(t, err)

				fromLoadable, err := reg.Schema(t.Context(), loadable)
				require.NoError(t, err)

				reg.Invalidate(tc.changed(file, loadable))

				again, err := reg.Schema(t.Context(), file)
				require.NoError(t, err)

				if tc.wantFileKept {
					assert.Same(t, fromFile, again)
				} else {
					assert.NotSame(t, fromFile, again)
				}

				again, err = reg.Schema(t.Context(), loadable)
				require.NoError(t, err)

				if tc.wantFileKept {
					assert.NotSame(t, fromLoadable, again)
				} else {
					assert.Same(t, fromLoadable, again)
				}

				assert.Equal(t, tc.wantLoads, loads.Load())
			})
		}
	})

	t.Run("file system", func(t *testing.T) {
		t.Parallel()

		files := map[string]string{"schemas/s.json": refA, "schemas/defs.json": stringDefs}
		changed, kept := yamltest.MapFS(files), yamltest.MapFS(files)

		reg := schema.NewRegistry()

		fromChanged, err := reg.Schema(t.Context(), schema.FileFS(changed, "schemas/s.json"))
		require.NoError(t, err)

		fromKept, err := reg.Schema(t.Context(), schema.FileFS(kept, "schemas/s.json"))
		require.NoError(t, err)

		changed["schemas/defs.json"] = &fstest.MapFile{Data: []byte(integerDefs)}
		kept["schemas/defs.json"] = &fstest.MapFile{Data: []byte(integerDefs)}

		reg.Invalidate(schema.FileFS(changed, "schemas/defs.json"))

		again, err := reg.Schema(t.Context(), schema.FileFS(changed, "schemas/s.json"))
		require.NoError(t, err)
		assert.NotSame(t, fromChanged, again)
		requireCheck(t, reg, schema.FileFS(changed, "schemas/s.json"), notInteger)

		again, err = reg.Schema(t.Context(), schema.FileFS(kept, "schemas/s.json"))
		require.NoError(t, err)
		assert.Same(t, fromKept, again)
	})

	t.Run("document a validation reached", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		yamltest.WriteFiles(t, dir, map[string]string{
			"s.json":    `{"properties": {"a": {"$ref": "mid.json"}}}`,
			"defs.json": stringDefs,
		})

		ref := schema.File(filepath.Join(dir, "s.json"))
		defs := schema.File(filepath.Join(dir, "defs.json"))

		// The schema compiles without mid.json. The registry drops the
		// failure of mid.json after a nanosecond, so a validation loads it
		// again.
		reg := schema.NewRegistry(requireRefs(false), schema.WithRetryAfter(time.Nanosecond))

		first, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		// The compile did not reach defs.json, so the registry keeps the
		// schema.
		reg.Invalidate(defs)

		again, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		require.Same(t, first, again)

		// The validation loads mid.json, and reaches defs.json through it.
		yamltest.WriteFile(t, dir, "mid.json", `{"$ref": "defs.json"}`)
		require.NoError(t, yamltest.FirstDocument(t, "a: x\n").Validate(t.Context(), first))

		reg.Invalidate(defs)

		again, err = reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.NotSame(t, first, again)
	})

	t.Run("load of a document a dropped schema reached", func(t *testing.T) {
		t.Parallel()

		gate := make(chan struct{})

		// The compile does not find mid.json. The second open of it is the
		// load of a validation, which waits for gate.
		fsys := &gatedFS{
			files:   yamltest.MapFS(map[string]string{"s.json": `{"properties": {"a": {"$ref": "mid.json"}}}`}),
			name:    "mid.json",
			started: make(chan struct{}, 1),
			gate:    gate,
			skip:    1,
		}

		ref := schema.FileFS(fsys, "s.json")
		reg := schema.NewRegistry(requireRefs(false), schema.WithRetryAfter(time.Nanosecond))

		held, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		// The registry drops the schema, and so stops tracking what a
		// validation with it reaches.
		reg.Invalidate(ref)

		fsys.files["mid.json"] = &fstest.MapFile{Data: []byte(stringDefs)}

		done := make(chan error, 1)

		go func() {
			done <- held.ValidateValue(t.Context(), map[string]any{"a": "x"})
		}()

		<-fsys.started

		// Only the load in flight reads mid.json, and the release finds it
		// there.
		reg.Invalidate(schema.FileFS(fsys, "mid.json"))

		close(gate)
		require.NoError(t, <-done)

		opens := fsys.opens.Load()

		// The registry did not keep the copy of mid.json that the load read
		// across the release, so the compile reads mid.json again.
		_, err = reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.Greater(t, fsys.opens.Load(), opens)
	})

	t.Run("concurrent use", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		yamltest.WriteFiles(t, dir, map[string]string{
			"s.json":    refA,
			"t.json":    `{"properties": {"a": {"$ref": "missing.json"}}}`,
			"defs.json": stringDefs,
		})

		refs := []schema.Ref{
			schema.File(filepath.Join(dir, "s.json")),
			schema.File(filepath.Join(dir, "t.json")),
		}
		defs := schema.File(filepath.Join(dir, "defs.json"))

		reg := schema.NewRegistry(schema.WithCacheLimit(1), requireRefs(false))

		var (
			wg   sync.WaitGroup
			errs [4]error
		)

		for g := range errs {
			wg.Go(func() {
				for i := range 100 {
					switch i % 10 {
					case 3:
						reg.Invalidate(defs)
					case 7:
						reg.Purge()
					}

					s, err := reg.Schema(t.Context(), refs[(g+i)%len(refs)])
					if err != nil {
						errs[g] = err

						return
					}

					// A validation of t.json reaches the $ref that did not
					// load, and fails.
					err = s.ValidateValue(t.Context(), map[string]any{"a": "x"})
					if err != nil && !errors.Is(err, schema.ErrValidate) {
						errs[g] = err

						return
					}
				}
			})
		}

		wg.Wait()

		require.NoError(t, errors.Join(errs[:]...))
	})
}

func TestRegistry_Purge(t *testing.T) {
	t.Parallel()

	t.Run("loads every schema again", func(t *testing.T) {
		t.Parallel()

		files := map[string]string{"s.json": refA, "defs.json": stringDefs}

		dir := t.TempDir()
		yamltest.WriteFiles(t, dir, files)

		fsys := &openCountingFS{files: yamltest.MapFS(files)}
		loadable, loads := countingLoader("loadable", []byte(stringA))

		refs := []schema.Ref{
			schema.File(filepath.Join(dir, "s.json")),
			schema.FileFS(fsys, "s.json"),
			loadable,
		}

		reg := schema.NewRegistry()

		first := make([]*schema.Schema, len(refs))

		for i, ref := range refs {
			s, err := reg.Schema(t.Context(), ref)
			require.NoError(t, err)

			first[i] = s
		}

		opens := fsys.opens.Load()

		// The registry remembers the failure of a file that did not exist.
		missing := schema.File(filepath.Join(dir, "missing.json"))

		_, err := reg.Schema(t.Context(), missing)
		requireLoadError(t, err, fs.ErrNotExist)

		yamltest.WriteFile(t, dir, "missing.json", stringA)

		reg.Purge()

		for i, ref := range refs {
			again, err := reg.Schema(t.Context(), ref)
			require.NoError(t, err)
			assert.NotSame(t, first[i], again)
		}

		// The schema and the document its $ref names load again.
		assert.Equal(t, 2*opens, fsys.opens.Load())
		assert.Equal(t, int32(2), loads.Load())

		_, err = reg.Schema(t.Context(), missing)
		require.NoError(t, err)
	})

	t.Run("stops holding every file system", func(t *testing.T) {
		t.Parallel()

		files := map[string]string{"s.json": refA, "defs.json": stringDefs}
		systems := []*heldFS{newHeldFS(files, nil), newHeldFS(files, asPointer)}

		reg := schema.NewRegistry()

		for _, h := range systems {
			_, err := reg.Schema(t.Context(), schema.FileFS(h.fsys, "s.json"))
			require.NoError(t, err)
		}

		reg.Purge()

		for _, h := range systems {
			assert.False(t, h.held())
		}

		runtime.KeepAlive(reg)
	})
}

func TestRegistry_ReleaseNested(t *testing.T) {
	t.Parallel()

	nestings := map[string]struct {
		// Builds the registry the program validates with around the
		// registry that loads the schema a directive names.
		nest func(inner *schema.Registry) *schema.Registry
		// Text the error of the check holds after the release, or "" for
		// a check that passes because the release did not reach inner.
		want string
	}{
		"registry": {
			nest: func(inner *schema.Registry) *schema.Registry {
				return schema.NewRegistry(schema.WithResolvers(inner))
			},
			want: notInteger,
		},
		"registry behind When": {
			nest: func(inner *schema.Registry) *schema.Registry {
				return schema.NewRegistry(schema.WithResolvers(
					schema.When(matcher.Content(kindPath, "App"), inner),
				))
			},
			want: notInteger,
		},
		"registry behind When behind When": {
			nest: func(inner *schema.Registry) *schema.Registry {
				return schema.NewRegistry(schema.WithResolvers(
					schema.When(
						matcher.Content(kindPath, "App"),
						schema.When(matcher.Content(kindPath, "App"), inner),
					),
				))
			},
			want: notInteger,
		},
		"registry behind a registry": {
			nest: func(inner *schema.Registry) *schema.Registry {
				mid := schema.NewRegistry(schema.WithResolvers(inner))

				return schema.NewRegistry(schema.WithResolvers(mid))
			},
			want: notInteger,
		},
		"registry that two resolvers hold": {
			nest: func(inner *schema.Registry) *schema.Registry {
				return schema.NewRegistry(schema.WithResolvers(
					schema.When(matcher.Content(kindPath, "Other"), inner),
					inner,
				))
			},
			want: notInteger,
		},
		// A release does not reach a registry that a resolver of the
		// program calls.
		"registry behind a ResolverFunc": {
			nest: func(inner *schema.Registry) *schema.Registry {
				return schema.NewRegistry(schema.WithResolvers(schema.ResolverFunc(inner.Resolve)))
			},
		},
	}

	releases := map[string]func(reg *schema.Registry, fsys fs.FS){
		"Invalidate": func(reg *schema.Registry, fsys fs.FS) {
			reg.Invalidate(schema.FileFS(fsys, "schemas/s.json"))
		},
		"Forget": func(reg *schema.Registry, fsys fs.FS) { reg.Forget(fsys) },
		"Purge":  func(reg *schema.Registry, _ fs.FS) { reg.Purge() },
	}

	for nestName, nc := range nestings {
		for releaseName, release := range releases {
			t.Run(nestName+"/"+releaseName, func(t *testing.T) {
				t.Parallel()

				fsys := yamltest.MapFS(map[string]string{
					"configs/app.yaml": yamltest.SchemaDirective("../schemas/s.json", "kind: App\na: x\n"),
					"schemas/s.json":   stringA,
				})

				inner := schema.NewRegistry(
					schema.WithAccess(schema.Unrestricted()),
					schema.WithResolvers(schema.Directive()),
				)
				reg := nc.nest(inner)

				check := func() error {
					source, err := niceyaml.NewSourceFromFS(fsys, "configs/app.yaml")
					require.NoError(t, err)

					//nolint:wrapcheck // The test inspects the error itself.
					return source.ValidateDocuments(t.Context(), reg)
				}

				require.NoError(t, check())

				fsys["schemas/s.json"] = &fstest.MapFile{Data: []byte(integerA)}

				// The registry serves the schema it compiled before the
				// change.
				require.NoError(t, check())

				release(reg, fsys)

				err := check()
				if nc.want == "" {
					require.NoError(t, err)

					return
				}

				require.ErrorContains(t, err, nc.want)
			})
		}
	}
}

func TestRegistry_ReleaseDuringLoad(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// Releases the document ref names from reg.
		release func(reg *schema.Registry, ref schema.Ref)
	}{
		"Invalidate": {
			release: func(reg *schema.Registry, ref schema.Ref) { reg.Invalidate(ref) },
		},
		"Purge": {
			release: func(reg *schema.Registry, _ schema.Ref) { reg.Purge() },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("schema", func(t *testing.T) {
				t.Parallel()

				type result struct {
					err error
					s   *schema.Schema
				}

				var loads atomic.Int32

				gate := make(chan struct{})
				started := make(chan struct{}, 2)

				ref := schema.Loadable("gated", func(context.Context) ([]byte, error) {
					loads.Add(1)

					started <- struct{}{}

					<-gate

					return []byte(stringA), nil
				})

				reg := schema.NewRegistry()

				request := func() <-chan result {
					out := make(chan result, 1)

					go func() {
						s, err := reg.Schema(t.Context(), ref)
						out <- result{s: s, err: err}
					}()

					return out
				}

				before := request()

				<-started

				tc.release(reg, ref)

				// A request after the release starts a load of its own.
				after := request()

				<-started

				close(gate)

				fromBefore, fromAfter := <-before, <-after
				require.NoError(t, fromBefore.err)
				require.NoError(t, fromAfter.err)
				assert.NotSame(t, fromBefore.s, fromAfter.s)

				// The load that started before the release kept nothing.
				again, err := reg.Schema(t.Context(), ref)
				require.NoError(t, err)
				assert.Same(t, fromAfter.s, again)
				assert.Equal(t, int32(2), loads.Load())
			})

			t.Run("document a $ref names", func(t *testing.T) {
				t.Parallel()

				gate := make(chan struct{})
				fsys := &gatedFS{
					files:   yamltest.MapFS(map[string]string{"s.json": refA, "defs.json": stringDefs}),
					name:    "defs.json",
					started: make(chan struct{}, 1),
					gate:    gate,
				}

				ref := schema.FileFS(fsys, "s.json")
				reg := schema.NewRegistry()

				done := make(chan error, 1)

				go func() {
					_, err := reg.Schema(t.Context(), ref)
					done <- err
				}()

				<-fsys.started

				tc.release(reg, schema.FileFS(fsys, "defs.json"))

				close(gate)
				require.NoError(t, <-done)

				opens := fsys.opens.Load()

				// The registry kept neither the schema nor the document the
				// load read across the release, so the compile reads the
				// document again.
				_, err := reg.Schema(t.Context(), ref)
				require.NoError(t, err)
				assert.Equal(t, 2*opens, fsys.opens.Load())
			})
		})
	}
}

// A registry confined to one directory never validates a document outside
// it against the schema at the same relative path inside. The schema
// beside the document takes only a string, and the one under the tenant
// takes anything, so the wrong schema would pass the document.
func TestRegistry_DocumentOutsideFileSystem(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	tenant := filepath.Join(base, "tenant")

	yamltest.WriteFiles(t, base, map[string]string{
		"shared/app.yaml":           yamltest.SchemaDirective("./schema.json", "name: 5\n"),
		"shared/schema.json":        `{"properties": {"name": {"type": "string"}}}`,
		"tenant/shared/schema.json": `{}`,
	})

	tcs := map[string]struct {
		err  error
		want string
		opts []schema.RegistryOption
		// The options of the source of the document.
		source []niceyaml.SourceOption
	}{
		"file system on the source": {
			opts:   []schema.RegistryOption{schema.WithAccess(schema.Unrestricted())},
			source: []niceyaml.SourceOption{niceyaml.WithFS(os.DirFS(tenant))},
			err:    schema.ErrFileNotAllowed,
			want:   "an absolute path names no file",
		},
		"AllowRoot": {
			opts: []schema.RegistryOption{withAccess(t, allowRoot(t, tenant))},
			err:  schema.ErrFileNotAllowed,
			want: "not under " + tenant,
		},
		"no file system": {
			opts: []schema.RegistryOption{schema.WithAccess(schema.Unrestricted())},
			want: `expected "string", got "integer"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source, err := niceyaml.NewSourceFromFile(filepath.Join(base, "shared", "app.yaml"), tc.source...)
			require.NoError(t, err)

			reg := schema.NewRegistry(append(tc.opts, schema.WithResolvers(schema.Directive()))...)

			err = source.ValidateDocuments(t.Context(), reg)
			require.ErrorContains(t, err, tc.want)

			if tc.err != nil {
				requireLoadError(t, err, tc.err)
			}
		})
	}
}

// A program can build a Ref with File, change directory, and only then
// validate through a registry with a file system or a root. The test
// changes the process's working directory, so it does not run in
// parallel.
//
//nolint:paralleltest // See above.
func TestRegistry_FileSystemAfterChdir(t *testing.T) {
	files := map[string]string{
		"schemas/config.json": `{"properties": {"a": {"$ref": "defs.json"}}}`,
		"schemas/defs.json":   `{"type": "string"}`,
	}

	bundle := yamltest.MapFS(files)

	const rel = "schemas/config.json"

	// At writes the files to wd and gives the registry a root there.
	at := func(t *testing.T, wd string) []schema.RegistryOption {
		t.Helper()

		yamltest.WriteFiles(t, wd, files)

		return []schema.RegistryOption{withAccess(t, allowRoot(t, wd))}
	}

	// FileFS reads the path as written, which no directory changes.
	// AllowRoot reads the path File made absolute, and the $ref resolves
	// to an absolute path beside it.
	tcs := map[string]struct {
		ref  func(wd string) schema.Ref
		opts func(t *testing.T, wd string) []schema.RegistryOption
	}{
		"relative path in a file system": {
			ref:  func(string) schema.Ref { return schema.FileFS(bundle, rel) },
			opts: func(*testing.T, string) []schema.RegistryOption { return nil },
		},
		"relative path under AllowRoot": {
			ref:  func(string) schema.Ref { return schema.File(rel) },
			opts: at,
		},
		"absolute path under AllowRoot": {
			ref:  func(wd string) schema.Ref { return schema.File(filepath.Join(wd, "schemas", "config.json")) },
			opts: at,
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			wd := t.TempDir()
			t.Chdir(wd)

			ref := tc.ref(wd)

			t.Chdir(t.TempDir())

			reg := schema.NewRegistry(append(tc.opts(t, wd), schema.WithResolvers(ref))...)

			requireAccepts(t, reg, "a: x\n")

			requireRejects(t, reg, "a: 5\n", `$.a: expected "string", got "integer"`)
		})
	}
}

// A Ref from FileFS reads the path it took, whatever directory FileFS
// ran in. Two Refs with one path share a Key and a schema, and two Refs
// with two paths name two files. The test changes the process's working
// directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestRegistry_FileFS_AnyWorkingDirectory(t *testing.T) {
	bundle := yamltest.MapFS(map[string]string{
		"x/s.json":    `{"type": "string"}`,
		"s.json":      `{"type": "integer"}`,
		"x/main.json": `{"$ref": "defs.json"}`,
		"x/defs.json": `{"type": "string"}`,
		"other.json":  `{"$ref": "defs.json"}`,
		"defs.json":   `{"type": "integer"}`,
	})

	// Each Ref is FileFS(bundle, path) run from dir under a temporary
	// base, and its schema accepts only values of the JSON type want.
	type fileRef struct {
		dir  string
		path string
		want string
	}

	tcs := map[string]struct {
		first  fileRef
		second fileRef
	}{
		"one path from two directories": {
			first:  fileRef{dir: ".", path: "s.json", want: "integer"},
			second: fileRef{dir: "x", path: "s.json", want: "integer"},
		},
		"two paths that name one file on disk": {
			first:  fileRef{dir: ".", path: "x/s.json", want: "string"},
			second: fileRef{dir: "x", path: "s.json", want: "integer"},
		},
		"one $ref from two directories": {
			first:  fileRef{dir: ".", path: "x/main.json", want: "string"},
			second: fileRef{dir: "x", path: "other.json", want: "integer"},
		},
	}

	values := map[string]any{"string": "x", "integer": 5}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(base, "x"), 0o700))

			files := []fileRef{tc.first, tc.second}
			refs := make([]schema.Ref, len(files))

			for i, f := range files {
				t.Chdir(filepath.Join(base, f.dir))

				refs[i] = schema.FileFS(bundle, f.path)
			}

			assert.Equal(t, files[0].path == files[1].path, refs[0].Key() == refs[1].Key())

			// Compile the two in both orders, each in a registry of its own.
			for _, order := range [][]int{{0, 1}, {1, 0}} {
				reg := schema.NewRegistry()
				schemas := make([]*schema.Schema, len(files))

				for _, i := range order {
					s, err := reg.Schema(t.Context(), refs[i])
					require.NoError(t, err)

					schemas[i] = s

					for typ, v := range values {
						err := s.ValidateValue(t.Context(), v)
						if typ == files[i].want {
							require.NoError(t, err, "ref %d, order %v", i, order)
						} else {
							require.Error(t, err, "ref %d, order %v", i, order)
						}
					}
				}

				// One path is one cache entry, whatever the Key.
				if files[0].path == files[1].path {
					assert.Same(t, schemas[0], schemas[1])
				} else {
					assert.NotSame(t, schemas[0], schemas[1])
				}
			}
		})
	}
}

func TestRegistry_Load(t *testing.T) {
	t.Parallel()

	t.Run("a URL ref fetches with the client", func(t *testing.T) {
		t.Parallel()

		base := serve(t, map[string]string{"/schema.json": `{"type": "object"}`})

		client, requests := yamltest.CountingClient(http.DefaultTransport)
		reg := schema.NewRegistry(schema.WithHTTPClient(client))

		data, err := reg.Load(t.Context(), schema.URL(base+"/schema.json"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("a YAML schema comes back as the bytes of its file", func(t *testing.T) {
		t.Parallel()

		const schemaData = "# Every document names itself.\nrequired: [name]\n"

		path := yamltest.WriteFile(t, t.TempDir(), "schema.yaml", schemaData)

		data, err := schema.NewRegistry().Load(t.Context(), schema.File(path))
		require.NoError(t, err)
		assert.Equal(t, schemaData, string(data))
	})

	t.Run("a compiled schema and the zero ref have no bytes", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		_, err := reg.Load(t.Context(), compileSchema(t, `{}`).Ref())
		require.ErrorIs(t, err, schema.ErrLoad)

		_, err = reg.Load(t.Context(), schema.Ref{})
		require.ErrorIs(t, err, schema.ErrLoad)
	})

	t.Run("an error is no fault of the document", func(t *testing.T) {
		t.Parallel()

		_, err := schema.NewRegistry().Load(t.Context(), loading("gone.json", nil, errors.New("schema is gone")))
		requireNoFault(t, err, schema.ErrLoad)
	})
}

func TestRegistry_Schema(t *testing.T) {
	t.Parallel()

	t.Run("compiles a loadable ref once and serves the cache after", func(t *testing.T) {
		t.Parallel()

		ref, loads := countingLoader("k", []byte(`{"type": "object", "required": ["port"]}`))

		reg := schema.NewRegistry()

		first, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		second, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.Same(t, first, second)
		assert.Equal(t, int32(1), loads.Load())

		// The schema validates a Go value as the one Lookup finds does.
		require.NoError(t, first.ValidateValue(t.Context(), map[string]any{"port": 80}))
		require.Error(t, first.ValidateValue(t.Context(), map[string]any{}))
	})

	t.Run("shares the cache with Lookup", func(t *testing.T) {
		t.Parallel()

		ref, loads := countingLoader("k", []byte(`{"type": "object"}`))

		reg := schema.NewRegistry(schema.WithResolvers(ref))

		fromLookup, err := reg.Lookup(t.Context(), document(t))
		require.NoError(t, err)

		fromRef, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.Same(t, fromLookup.Schema, fromRef)
		assert.Equal(t, int32(1), loads.Load())
	})

	t.Run("compiles with the registry's compile options", func(t *testing.T) {
		t.Parallel()

		ref := loading("k", []byte(`{"type": "string", "format": "email"}`), nil)

		plain, err := schema.NewRegistry().Schema(t.Context(), ref)
		require.NoError(t, err)
		require.NoError(t, plain.ValidateValue(t.Context(), "not an email"))

		strict := schema.NewRegistry(schema.WithCompileOptions(
			schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)),
		))

		checked, err := strict.Schema(t.Context(), ref)
		require.NoError(t, err)
		require.Error(t, checked.ValidateValue(t.Context(), "not an email"))
	})

	t.Run("a compiled schema comes back as it is", func(t *testing.T) {
		t.Parallel()

		compiled := compileSchema(t, `{}`)

		got, err := schema.NewRegistry().Schema(t.Context(), compiled.Ref())
		require.NoError(t, err)
		assert.Same(t, compiled, got)
	})

	t.Run("the zero ref names no schema", func(t *testing.T) {
		t.Parallel()

		_, err := schema.NewRegistry().Schema(t.Context(), schema.Ref{})
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorContains(t, err, "names no schema")
		assert.NotContains(t, err.Error(), "resolver")
	})

	t.Run("a load that fails is ErrLoad", func(t *testing.T) {
		t.Parallel()

		_, err := schema.NewRegistry().Schema(t.Context(), loading("k", nil, errors.New("boom")))
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.True(t, strings.HasPrefix(err.Error(), `load schema: "k": `), err.Error())
	})

	t.Run("bytes that do not compile are ErrCompile", func(t *testing.T) {
		t.Parallel()

		_, err := schema.NewRegistry().Schema(t.Context(), loading("k", []byte(`{"type": 42}`), nil))
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.True(t, strings.HasPrefix(err.Error(), `compile schema: "k": `), err.Error())
	})

	t.Run("a load whose bytes do not compile runs once in the retry interval", func(t *testing.T) {
		t.Parallel()

		ref, loads := countingLoader("k", []byte(`{"type": 42}`))

		reg := schema.NewRegistry()

		for range 3 {
			_, err := reg.Schema(t.Context(), ref)
			require.ErrorIs(t, err, schema.ErrCompile)
			assert.True(t, strings.HasPrefix(err.Error(), `compile schema: "k": `), err.Error())
		}

		assert.Equal(t, int32(1), loads.Load())
	})

	t.Run("an error is no fault of the document", func(t *testing.T) {
		t.Parallel()

		errGone := errors.New("schema is gone")
		reg := schema.NewRegistry()

		// A check that loads a schema itself returns the error of the
		// load or the compile as it is.
		_, err := reg.Schema(t.Context(), loading("gone.json", nil, errGone))
		requireNoFault(t, err, schema.ErrLoad)

		_, err = reg.Schema(t.Context(), loading("bad.json", []byte(`{"type": 42}`), nil))
		requireNoFault(t, err, schema.ErrCompile)
	})
}

// kindError is the reason of a resolver that has no schema for a kind, of
// a type a caller reads the kind from.
type kindError struct {
	kind string
}

func (e *kindError) Error() string {
	return schema.ErrNoMatch.Error() + ": no schema for kind " + e.kind
}

func (e *kindError) Unwrap() error {
	return schema.ErrNoMatch
}

func TestRegistry_Schema_LoadableKey(t *testing.T) {
	t.Parallel()

	// The URL or the file holds fetched. The Ref from Loadable serves
	// vendored under the key of that URL or file.
	const (
		schemaURL = "https://schemas.test/s.json"
		fetched   = `{"type": "string"}`
		vendored  = `{"type": "integer"}`
	)

	requireFetched := func(t *testing.T, s *schema.Schema) {
		t.Helper()

		require.NoError(t, s.ValidateValue(t.Context(), "x"))
		require.Error(t, s.ValidateValue(t.Context(), 5))
	}

	requireVendored := func(t *testing.T, s *schema.Schema) {
		t.Helper()

		require.NoError(t, s.ValidateValue(t.Context(), 5))
		require.Error(t, s.ValidateValue(t.Context(), "x"))
	}

	tcs := map[string]struct {
		// Builds the Ref that names the location, and the options that a
		// registry reaches the location with.
		located func(t *testing.T) (schema.Ref, []schema.RegistryOption)
		// The error of a registry whose access refuses the location.
		refused error
	}{
		"URL": {
			located: func(*testing.T) (schema.Ref, []schema.RegistryOption) {
				client, _ := memoryClient(map[string]string{"/s.json": fetched})

				return schema.URL(schemaURL), []schema.RegistryOption{schema.WithHTTPClient(client)}
			},
			refused: schema.ErrURLNotAllowed,
		},
		"file": {
			located: func(t *testing.T) (schema.Ref, []schema.RegistryOption) {
				t.Helper()

				return schema.File(yamltest.WriteFile(t, t.TempDir(), "s.json", fetched)), nil
			},
			refused: schema.ErrFileNotAllowed,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("each Ref gets its own bytes in either order", func(t *testing.T) {
				t.Parallel()

				for _, loadableFirst := range []bool{true, false} {
					located, opts := tc.located(t)
					loadable, loads := countingLoader(located.Key(), []byte(vendored))

					reg := schema.NewRegistry(opts...)

					if loadableFirst {
						s, err := reg.Schema(t.Context(), loadable)
						require.NoError(t, err)
						requireVendored(t, s)
					}

					fromLocation, err := reg.Schema(t.Context(), located)
					require.NoError(t, err)
					requireFetched(t, fromLocation)

					fromLoadable, err := reg.Schema(t.Context(), loadable)
					require.NoError(t, err)
					requireVendored(t, fromLoadable)

					assert.Equal(t, int32(1), loads.Load())
				}
			})

			t.Run("a refused location does not fail the loadable", func(t *testing.T) {
				t.Parallel()

				located, opts := tc.located(t)
				loadable, loads := countingLoader(located.Key(), []byte(vendored))

				// The access does not allow any file or URL.
				reg := schema.NewRegistry(append(opts, withAccess(t))...)

				_, err := reg.Schema(t.Context(), located)
				requireLoadError(t, err, tc.refused)

				s, err := reg.Schema(t.Context(), loadable)
				require.NoError(t, err)
				requireVendored(t, s)
				assert.Equal(t, int32(1), loads.Load())
			})

			t.Run("the loadable does not answer for a refused location", func(t *testing.T) {
				t.Parallel()

				located, opts := tc.located(t)
				loadable := loading(located.Key(), []byte(vendored), nil)

				reg := schema.NewRegistry(append(opts, withAccess(t))...)

				_, err := reg.Schema(t.Context(), loadable)
				require.NoError(t, err)

				_, err = reg.Schema(t.Context(), located)
				requireLoadError(t, err, tc.refused)
			})
		})
	}

	t.Run("a refused directive does not fail the loadable", func(t *testing.T) {
		t.Parallel()

		loadable, loads := countingLoader(schemaURL, []byte(vendored))

		reg := schema.NewRegistry(
			withAccess(t, schema.AllowURLPrefixes("https://only.test/")),
			schema.WithResolvers(schema.Directive()),
		)

		doc := yamltest.FirstDocument(t, yamltest.SchemaDirective(schemaURL, "a: 1\n"))

		err := reg.Check(t.Context(), doc)
		requireLoadError(t, err, schema.ErrURLNotAllowed)

		s, err := reg.Schema(t.Context(), loadable)
		require.NoError(t, err)
		requireVendored(t, s)
		assert.Equal(t, int32(1), loads.Load())
	})
}

func TestRegistry_Lookup_NoMatchReasons(t *testing.T) {
	t.Parallel()

	errNoKind := fmt.Errorf("%w: no kind", schema.ErrNoMatch)

	byKind := failing(errNoKind)

	// A document no resolver of these subtests has a schema for.
	doc := yamltest.FirstDocumentWithPath(t, "kind: Service\n", "app.yaml")

	t.Run("gives the reason of each resolver that gave one as a detail", func(t *testing.T) {
		t.Parallel()

		embedded := schema.Embedded([]byte(`{}`))
		isPod := matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) { return false, nil })

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(
			schema.Directive(),
			schema.When(matcher.MustFilePath("menus/*.{yaml,yml}"), embedded),
			// A Func does not describe itself, so its guard gives no reason.
			schema.When(isPod, embedded),
			schema.When(matcher.Content(kindPath, "Deployment"), embedded),
			byKind,
		))

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)
		require.ErrorIs(t, err, errNoKind)
		// The lookup failed once, so the message is one line, and the
		// reasons explain it in the tree below. A resolver that returned
		// ErrNoMatch alone adds no reason, and the reasons read without the
		// sentinel the message states already.
		require.EqualError(t, err, "app.yaml: no matching schema")

		wantTree := stringtest.JoinLF(
			"app.yaml: no matching schema",
			"|-- no schema directive",
			`|-- applies only when file path matches "menus/*.{yaml,yml}"`,
			`|-- applies only when @.kind is "Deployment"`,
			"`-- no kind",
		)
		assert.Equal(t, wantTree, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
		assert.Equal(t, wantTree, fmt.Sprintf("%+v", err))

		bound := yamltest.RequireSourceError(t, err)

		assert.Empty(t, yamltest.ProblemsOf(bound), "a reason is a detail, not a problem the error heads")

		var reasons []string

		for _, detail := range yamltest.DetailsOf(bound) {
			reasons = append(reasons, detail.Bound.Unwrap().Error())
		}

		assert.Equal(t, []string{
			"no schema directive",
			`applies only when file path matches "menus/*.{yaml,yml}"`,
			`applies only when @.kind is "Deployment"`,
			"no kind",
		}, reasons)

		assert.Len(t, yamltest.OnlyProblem(t, err).Children, 4)
	})

	t.Run("names the document in a file that holds several", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			b: 2
			---
			kind: Wat
		`), niceyaml.WithName("m.yaml"))

		docs := yamltest.Documents(t, source, 3)

		err := docs[2].Validate(t.Context(), reg)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.EqualError(t, err, "m.yaml: document 3: no matching schema")

		wantTree := stringtest.JoinLF(
			"m.yaml: document 3: no matching schema",
			"`-- no schema directive",
		)
		assert.Equal(t, wantTree, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
		assert.Equal(t, wantTree, fmt.Sprintf("%+v", err))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "no matching schema", bound.Message())

		index, ok := bound.DocumentIndex()
		require.True(t, ok)
		assert.Equal(t, 2, index)
	})

	t.Run("reports ErrNoMatch alone when no resolver says more", func(t *testing.T) {
		t.Parallel()

		isPod := matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) { return false, nil })
		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(isPod, schema.Embedded([]byte(`{}`))),
			failing(schema.ErrNoMatch),
		))

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		assert.Equal(t, "app.yaml: no matching schema", err.Error())

		bound := yamltest.RequireSourceError(t, err)

		assert.Empty(t, yamltest.ProblemsOf(bound), "a reason is a detail, not a problem the error heads")
	})

	t.Run("matches each reason, which is a detail the error does not unwrap to", func(t *testing.T) {
		t.Parallel()

		typed := failing(&kindError{kind: "Service"})
		reg := schema.NewRegistry(
			schema.WithAccess(schema.Unrestricted()),
			schema.WithResolvers(schema.Directive(), typed, byKind),
		)

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)
		require.ErrorIs(t, err, errNoKind)

		reason, ok := errors.AsType[*kindError](err)
		require.True(t, ok)
		assert.Equal(t, "Service", reason.kind)

		// The error matches what a reason matches and nothing more.
		require.NotErrorIs(t, err, schema.ErrResolve)

		var pathErr *fs.PathError

		require.NotErrorAs(t, err, &pathErr)

		// The one row of a report matches the reasons the same way, and
		// each reason stays a detail below it.
		problem := yamltest.OnlyProblem(t, err)
		require.ErrorIs(t, problem.Err, schema.ErrNoDirective)
		require.Len(t, problem.Children, 3)
		assert.Equal(t, "no schema for kind Service", problem.Children[1].Message())
	})

	t.Run("matches what a reason wraps except the error of a context", func(t *testing.T) {
		t.Parallel()

		// The resolver gave up under a deadline of its own, and the
		// context of the lookup goes on.
		timedOut := failing(fmt.Errorf("%w: read catalog: %w", schema.ErrNoMatch, context.DeadlineExceeded))
		canceled := failing(fmt.Errorf("%w: %w", schema.ErrNoMatch, context.Canceled))
		errNoLabel := fmt.Errorf("%w: no label", schema.ErrNoMatch)
		// A sentinel that does not wrap ErrNoMatch, joined beside it.
		errNoOwner := errors.New("no owner")
		reg := schema.NewRegistry(schema.WithResolvers(
			timedOut,
			canceled,
			failing(errNoLabel),
			failing(fmt.Errorf("%w: %w", schema.ErrNoMatch, errNoOwner)),
		))

		_, err := reg.Lookup(t.Context(), doc)
		require.EqualError(t, err, "app.yaml: no matching schema")
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, errNoLabel)
		require.ErrorIs(t, err, errNoOwner)
		// The error of the resolver's own context stays a detail. The
		// no-match does not match it, so the document stays at fault.
		require.NotErrorIs(t, err, context.DeadlineExceeded)
		require.NotErrorIs(t, err, context.Canceled)
		assert.True(t, niceyaml.IsInvalid(err))
		assert.Equal(t,
			stringtest.JoinLF(
				"app.yaml: no matching schema",
				"|-- read catalog: context deadline exceeded",
				"|-- context canceled",
				"|-- no label",
				"`-- no owner",
			),
			niceyaml.FormatError(err, niceyaml.WithContextLines(0)),
		)
	})

	t.Run("Check passes the reasons through when it requires a schema", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))

		doc := yamltest.FirstDocument(t, "kind: Service\n")

		err := reg.Check(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)

		lenient := schema.NewRegistry(
			schema.WithAccess(schema.Unrestricted()),
			schema.WithResolvers(schema.Directive()),
			schema.WithRequireSchema(false),
		)
		require.NoError(t, lenient.Check(t.Context(), doc))
	})
}

// A directive names its schema in the text of the document, so a lookup
// binds each error of that schema, and each error of the reference, at
// the comment that holds the directive.
func TestRegistry_Lookup_AtDirective(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	yamltest.WriteFile(t, dir, "broken.json", `{"type": 5}`)

	app := filepath.Join(dir, "app.yaml")
	plain := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))
	confined := schema.NewRegistry(withAccess(t), schema.WithResolvers(schema.Directive()))
	nested := schema.NewRegistry(schema.WithResolvers(confined))

	tcs := map[string]struct {
		reg   *schema.Registry
		input string
		// The file path of the document, or "" for one without.
		filePath string
		// The index of the document that the lookup runs for.
		index int
		// The first line of the message.
		want string
		// The position of the directive.
		at  position.Position
		err []error
	}{
		"schema that does not load": {
			reg:      plain,
			input:    yamltest.SchemaDirective("./missing.json", "a: 1\n"),
			filePath: app,
			want:     `app.yaml:1:1: load schema: "` + plainFileURL(filepath.Join(dir, "missing.json")) + `"`,
			err:      []error{schema.ErrLoad, fs.ErrNotExist},
		},
		"schema that does not compile": {
			reg:      plain,
			input:    yamltest.SchemaDirective("./broken.json", "a: 1\n"),
			filePath: app,
			want:     `app.yaml:1:1: compile schema: "` + plainFileURL(filepath.Join(dir, "broken.json")) + `"`,
			err:      []error{schema.ErrCompile},
		},
		"directive below another document": {
			reg:      plain,
			input:    "a: 1\n---\n" + yamltest.SchemaDirective("./missing.json", "b: 2\n"),
			filePath: app,
			index:    1,
			want:     `app.yaml:3:1: load schema: "` + plainFileURL(filepath.Join(dir, "missing.json")) + `"`,
			at:       position.New(2, 0),
			err:      []error{schema.ErrLoad},
		},
		"URL that a confined registry refuses": {
			reg:   nested,
			input: yamltest.SchemaDirective("https://evil.test/s.json", "a: 1\n"),
			want:  `app.yaml:1:1: load schema: "https://evil.test/s.json": fetch https://evil.test/s.json: URL not allowed`,
			err:   []error{schema.ErrLoad, schema.ErrURLNotAllowed},
		},
		"relative directive without a file path": {
			reg:   plain,
			input: yamltest.SchemaDirective("./s.json", "a: 1\n"),
			want:  "app.yaml:1:1: resolve schema: document has no file path",
			err:   []error{schema.ErrResolve, schema.ErrNoFilePath, schema.ErrNoBaseDir},
		},
		"relative directive without a file path in a nested registry": {
			reg:   nested,
			input: yamltest.SchemaDirective("./s.json", "a: 1\n"),
			want:  "app.yaml:1:1: resolve schema: document has no file path",
			err:   []error{schema.ErrResolve, schema.ErrNoFilePath},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{niceyaml.WithName("app.yaml")}
			if tc.filePath != "" {
				opts = append(opts, niceyaml.WithFilePath(tc.filePath))
			}

			source := niceyaml.NewSourceFromString(tc.input, opts...)
			doc := source.Documents()[tc.index]

			_, err := tc.reg.Lookup(t.Context(), doc)
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), tc.want), "got %q", err.Error())

			for _, target := range tc.err {
				require.ErrorIs(t, err, target)
			}

			// The program set up the registry, so the document is not at
			// fault for any of these errors.
			assert.False(t, niceyaml.IsInvalid(err))

			at, ok := yamltest.RequireSourceError(t, err).Position()
			require.True(t, ok)
			assert.Equal(t, tc.at, at)
		})
	}

	t.Run("each document binds at its own directive", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.JoinLF(
			"# yaml-language-server: $schema=./broken.json",
			"a: 1",
			"---",
			"# yaml-language-server: $schema=./broken.json",
			"b: 2",
		), niceyaml.WithName("app.yaml"), niceyaml.WithFilePath(app))

		docs := yamltest.Documents(t, source, 2)

		// The registry remembers the failed compile of the first lookup, and
		// the second lookup binds that failure at its own directive.
		for i, want := range []string{"app.yaml:1:1: compile schema:", "app.yaml:4:1: compile schema:"} {
			_, err := plain.Lookup(t.Context(), docs[i])
			require.ErrorIs(t, err, schema.ErrCompile)
			assert.True(t, strings.HasPrefix(err.Error(), want), "got %q", err.Error())
		}
	})

	t.Run("a Ref that another document reuses binds without a position", func(t *testing.T) {
		t.Parallel()

		base := niceyaml.NewSourceFromString(stringtest.JoinLF(
			"# a",
			"# b",
			"# yaml-language-server: $schema=./missing.json",
			"a: 1",
		), niceyaml.WithName("base.yaml"), niceyaml.WithFilePath(app))

		ref, err := schema.Directive().Resolve(t.Context(), base.Documents()[0])
		require.NoError(t, err)

		reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(ref))

		// The directive stands on the third line of base.yaml, and the
		// third line of other.yaml holds a value that does not name any
		// schema.
		other := niceyaml.NewSourceFromString(stringtest.JoinLF(
			"name: x",
			"kind: App",
			"spec:",
			"  a: 1",
		), niceyaml.WithName("other.yaml"), niceyaml.WithFilePath(filepath.Join(dir, "other.yaml")))

		_, err = reg.Lookup(t.Context(), other.Documents()[0])
		requireLoadError(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), "other.yaml: load schema:"), "got %q", err.Error())

		_, ok := yamltest.RequireSourceError(t, err).Position()
		assert.False(t, ok)

		// The document that holds the directive still binds at it.
		_, err = reg.Lookup(t.Context(), base.Documents()[0])
		requireLoadError(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), "base.yaml:3:1: load schema:"), "got %q", err.Error())
	})

	t.Run("a schema that a guard names binds without a position", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Exists(paths.Doc().Child("a")), schema.File(filepath.Join(dir, "missing.json"))),
		))

		source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("app.yaml"), niceyaml.WithFilePath(app))

		_, err := reg.Lookup(t.Context(), source.Documents()[0])
		requireLoadError(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), "app.yaml: load schema:"), "got %q", err.Error())

		_, ok := yamltest.RequireSourceError(t, err).Position()
		assert.False(t, ok)
	})

	t.Run("the directive of a merged document binds without a position", func(t *testing.T) {
		t.Parallel()

		// The merged text starts with the preamble of the layer that the
		// Source stands for. No value lies at the directive, so the error
		// binds in that layer without a position.
		layers := t.TempDir()
		base := yamltest.WriteFile(t, layers, "base.yaml", yamltest.SchemaDirective("./missing.json", "a: 1\nb: 2\n"))
		prod := yamltest.WriteFile(t, layers, "prod.yaml", "b: 3\n")

		var sources []*niceyaml.Source

		for _, path := range []string{base, prod} {
			source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithName(filepath.Base(path)))
			require.NoError(t, err)

			sources = append(sources, source)
		}

		merged, err := niceyaml.NewSourceFromLayers(t.Context(), sources)
		require.NoError(t, err)

		_, err = plain.Lookup(t.Context(), merged.Documents()[0])
		requireLoadError(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), "base.yaml: load schema:"), "got %q", err.Error())

		_, ok := yamltest.RequireSourceError(t, err).Position()
		assert.False(t, ok)
	})
}

func TestRegistry_Schema_RequireRefs(t *testing.T) {
	t.Parallel()

	const (
		rootSchema = `{"properties": {"a": {"$ref": "defs.json#/$defs/a"}}}`
		twoSchema  = `{"allOf": [{"$ref": "one.json"}, {"$ref": "other.json"}]}`
	)

	tmpDir := t.TempDir()
	yamltest.WriteFile(t, tmpDir, "root.json", rootSchema)

	bundle := yamltest.MapFS(map[string]string{
		"schemas/root.json": rootSchema,
		"schemas/two.json":  twoSchema,
	})

	base := serve(t, map[string]string{"/s/root.json": rootSchema})

	// A file URL takes a fragment, which a plain path does not.
	rootURL := plainFileURL(filepath.Join(tmpDir, "root.json"))

	fragment, err := schema.FileOrURL(tmpDir, rootURL+"#/properties/a")
	require.NoError(t, err)

	tcs := map[string]struct {
		// The sentinel of the error, or nil for a schema that loads.
		err error
		// An error the load error wraps besides the sentinel.
		cause error
		ref   schema.Ref
		// Text the message of the error holds.
		want string
		opts []schema.RegistryOption
	}{
		"file on disk": {
			ref:   schema.File(filepath.Join(tmpDir, "root.json")),
			err:   schema.ErrLoad,
			cause: fs.ErrNotExist,
			want:  "cannot resolve $ref: read " + filepath.Join(tmpDir, "defs.json"),
		},
		"fragment of a file on disk": {
			ref:   fragment,
			err:   schema.ErrLoad,
			cause: fs.ErrNotExist,
			want:  "cannot resolve $ref: read " + filepath.Join(tmpDir, "defs.json"),
		},
		"file in a file system": {
			ref:   schema.FileFS(bundle, "schemas/root.json"),
			err:   schema.ErrLoad,
			cause: fs.ErrNotExist,
			want: `load schema: "schemas/root.json": cannot resolve $ref: ` +
				`read schemas/defs.json: open schemas/defs.json: file does not exist`,
		},
		"url": {
			ref:  schema.URL(base + "/s/root.json"),
			err:  schema.ErrLoad,
			want: `/s/root.json": cannot resolve $ref: `,
		},
		"two documents": {
			ref:   schema.FileFS(bundle, "schemas/two.json"),
			err:   schema.ErrLoad,
			cause: fs.ErrNotExist,
			want: stringtest.JoinLF(
				`load schema: "schemas/two.json": cannot resolve $ref: `+
					"read schemas/one.json: open schemas/one.json: file does not exist",
				"read schemas/other.json: open schemas/other.json: file does not exist",
			),
		},
		// The registry resolves no $ref of a schema it holds as bytes, so
		// the schema does not compile, as it does not in Compile.
		"embedded bytes": {
			ref:   schema.Embedded([]byte(rootSchema)),
			err:   schema.ErrCompile,
			cause: jsonschema.ErrNotResolved,
			want:  `no ref resolver for $ref to "defs.json": schema URI not resolved`,
		},
		"refs not required": {
			opts: []schema.RegistryOption{requireRefs(false)},
			ref:  schema.FileFS(bundle, "schemas/root.json"),
		},
		// A resolver of the caller's does not replace the registry's for
		// a schema that has a location, so the registry still finds the
		// missing document.
		"resolver of the caller's that does not serve the document": {
			opts: []schema.RegistryOption{schema.WithCompileOptions(schema.WithJSONSchemaOptions(
				jsonschema.WithRefResolver(jsonschema.SchemaMap{}),
			))},
			ref:   schema.FileFS(bundle, "schemas/root.json"),
			err:   schema.ErrLoad,
			cause: fs.ErrNotExist,
			want:  `load schema: "schemas/root.json": cannot resolve $ref: read schemas/defs.json`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(tc.opts...)

			s, err := reg.Schema(t.Context(), tc.ref)
			if tc.err == nil {
				require.NoError(t, err)

				err = yamltest.FirstDocument(t, "a: 5\n").Validate(t.Context(), s)
				require.ErrorIs(t, err, schema.ErrValidate)

				return
			}

			require.ErrorIs(t, err, tc.err)
			assert.Nil(t, s)
			assert.Contains(t, err.Error(), tc.want)

			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}

			// The registry answers a document that names the schema the
			// same way, bound to the document.
			byDocument := schema.NewRegistry(append(tc.opts, schema.WithResolvers(tc.ref))...)

			err = checkInput(t, byDocument, "b: 5\n")
			require.ErrorIs(t, err, tc.err)
			assert.False(t, niceyaml.IsInvalid(err))
		})
	}

	t.Run("a miss under a context that ended is not kept", func(t *testing.T) {
		t.Parallel()

		// The referenced document answers once release closes. A request
		// before then waits for it, or for its own context to end.
		entered := make(chan struct{}, 1)
		release := make(chan struct{})

		files := schemaHandler(map[string]string{
			"/root.json": `{"properties": {"a": {"$ref": "slow.json"}}}`,
			"/slow.json": `{"type": "string"}`,
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow.json" {
				select {
				case entered <- struct{}{}:
				default:
				}

				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}

			files.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)

		ref := schema.URL(server.URL + "/root.json")
		reg := schema.NewRegistry()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go func() {
			<-entered
			cancel()
		}()

		_, err := reg.Schema(ctx, ref)
		requireLoadError(t, err, context.Canceled)

		close(release)

		s, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		err = yamltest.FirstDocument(t, "a: 5\n").Validate(t.Context(), s)
		require.ErrorContains(t, err, `expected "string"`)
	})
}

func TestRegistry_Schema_CallerRefResolver(t *testing.T) {
	t.Parallel()

	const (
		// The schema names a definition in the document beside it.
		rootSchema = `{"properties": {"a": {"$ref": "defs.json#/$defs/a"}}}`
		// The document beside the schema, which requires an integer.
		defsSchema = `{"$defs": {"a": {"type": "integer"}}}`
		// The document that the caller's options point to, which requires
		// a string.
		elsewhereSchema = `{"$defs": {"a": {"type": "string"}}}`
	)

	bundle := yamltest.MapFS(map[string]string{
		"schemas/root.json": rootSchema,
		"schemas/defs.json": defsSchema,
	})

	base := serve(t, map[string]string{"/root.json": rootSchema, "/defs.json": defsSchema})
	elsewhere := serve(t, map[string]string{"/defs.json": elsewhereSchema})

	// The caller's resolver serves every URI with the document that
	// requires a string.
	serving := jsonschema.RefResolverFunc(func(context.Context, string) (*jsonschema.Schema, error) {
		return jsonschema.ParseSchema([]byte(elsewhereSchema))
	})

	resolverOpts := schema.WithCompileOptions(schema.WithJSONSchemaOptions(jsonschema.WithRefResolver(serving)))

	withBase := func(uri string) schema.RegistryOption {
		return schema.WithCompileOptions(schema.WithJSONSchemaOptions(jsonschema.WithBaseURI(uri)))
	}

	// Each set of options takes the URL of the document that the Ref
	// names. Options that replaced the registry's own resolve the $ref to
	// the document that requires a string, or point the $ref of a
	// fragment Ref back at itself.
	callers := map[string]func(docURL string) schema.RegistryOption{
		"ref resolver": func(string) schema.RegistryOption { return resolverOpts },
		"base uri": func(string) schema.RegistryOption {
			return withBase(elsewhere + "/x.json")
		},
		"base uri of document": withBase,
	}

	// Every Ref that has a location resolves its $ref through the
	// registry, so the document beside the schema decides.
	located := map[string]struct {
		ref    schema.Ref
		docURL string
	}{
		"file system": {
			ref:    schema.FileFS(bundle, "schemas/root.json"),
			docURL: "file:///schemas/root.json",
		},
		"fragment in file system": {
			ref:    schema.FileFS(bundle, "schemas/root.json").WithFragment("/properties/a"),
			docURL: "file:///schemas/root.json",
		},
		"url": {
			ref:    schema.URL(base + "/root.json"),
			docURL: base + "/root.json",
		},
		"fragment of url": {
			ref:    schema.URL(base + "/root.json").WithFragment("/properties/a"),
			docURL: base + "/root.json",
		},
	}

	for caller, opts := range callers {
		for name, loc := range located {
			t.Run(caller+"/"+name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(opts(loc.docURL), schema.WithResolvers(loc.ref))

				accepted, rejected := "a: 5\n", "a: x\n"
				if strings.Contains(name, "fragment") {
					accepted, rejected = "5\n", "x\n"
				}

				requireAccepts(t, reg, accepted)
				requireRejects(t, reg, rejected, `expected "integer"`)
			})
		}
	}

	// A schema without a location has no other resolver, so the caller's
	// serves its $ref.
	t.Run("embedded", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(resolverOpts, schema.WithResolvers(schema.Embedded([]byte(rootSchema))))

		requireAccepts(t, reg, "a: x\n")
		requireRejects(t, reg, "a: 5\n", `expected "string"`)
	})
}

func TestRegistry_Schema_LoadExitReachesCaller(t *testing.T) {
	t.Parallel()

	// The group runs the load on a goroutine of its own, so a load that
	// exits without a result must end the caller's goroutine the same way.
	// The caller can recover a panic there, and a call to runtime.Goexit,
	// as t.FailNow makes, must not leave the caller waiting on a load that
	// never answers. The call runs inside capture.Run and drops what Schema
	// returns, so only a panic or a call to runtime.Goexit yields an error.
	tcs := map[string]struct {
		ref schema.Ref
		err error
	}{
		"panic": {
			ref: schema.Loadable("boom.json", func(context.Context) ([]byte, error) {
				panic("boom")
			}),
			err: &capture.PanicError{Value: "boom"},
		},
		"runtime.Goexit": {
			ref: schema.Loadable("goexit.json", func(context.Context) ([]byte, error) {
				runtime.Goexit()

				return nil, nil
			}),
			err: capture.ErrGoexit,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry()

			// The second call loads again rather than hanging on the failed
			// load.
			for range 2 {
				ended := make(chan error, 1)

				go func() {
					ended <- capture.Run(func() error {
						//nolint:errcheck // The call must end through a panic or runtime.Goexit.
						_, _ = reg.Schema(t.Context(), tc.ref)

						return nil
					})
				}()

				select {
				case err := <-ended:
					assert.Equal(t, tc.err, err)
				case <-time.After(5 * time.Second):
					require.FailNow(t, "Schema hung after the load of "+tc.ref.Key()+" exited")
				}
			}
		})
	}
}

func TestRegistry_FragmentRefs(t *testing.T) {
	t.Parallel()

	// The root takes any object, and each subschema requires a member.
	// Foo reaches Name through a pointer $ref, which resolves against the
	// root of the document rather than against Foo.
	const defsSchema = `{
		"type": "object",
		"$defs": {
			"Foo": {"$ref": "#/$defs/Name"},
			"Name": {"required": ["name"]},
			"Bar": {"required": ["id"]},
			"Named": {"$anchor": "named", "required": ["name"]}
		}
	}`

	// Names under .test never resolve, so a fetch that bypasses the
	// registry's client fails rather than going uncounted.
	const baseURL = "http://schemas.test"

	// Each subtest takes a client of its own, which serves defs.json from
	// memory and counts every request it sends, whatever the path or host.
	// The client also answers a spelling of the path with dot segments, as
	// a server that removes them does.
	newClient := func() (*http.Client, *atomic.Int32) {
		return memoryClient(map[string]string{
			"/defs.json":      defsSchema,
			"/x/../defs.json": defsSchema,
		})
	}

	// A caller's resolver that serves an unrelated URN. The registry
	// alone holds the document a fragment names, so this resolver must
	// not replace the registry's.
	urnResolver := schema.WithCompileOptions(schema.WithJSONSchemaOptions(
		jsonschema.WithRefResolver(jsonschema.SchemaMap{"urn:x": {}}),
	))

	// The registry fetches the document of each URL once. Every fragment
	// but the empty one names a subschema that requires a name. A case
	// that sets no document takes "name: x" as the valid one and "other: 1"
	// as the invalid one, and wants the message of the missing name.
	tcs := map[string]struct {
		url     string
		valid   string
		invalid string
		want    string
		opts    []schema.RegistryOption
	}{
		"url pointer": {url: baseURL + "/defs.json#/$defs/Foo"},
		"url pointer with an escaped unreserved character": {url: baseURL + "/%64efs.json#/$defs/Foo"},
		"url pointer with a dot segment":                   {url: baseURL + "/x/../defs.json#/$defs/Foo"},
		"url pointer with an upper-case host": {
			url: "http://Schemas.Test/defs.json#/$defs/Foo",
		},
		// The registry decodes the unreserved %7e and spells the reserved
		// %2f in upper-case hex, as the compiler does.
		"url pointer with escapes in the query": {url: baseURL + "/defs.json?v=%7e1&q=%2f#/$defs/Foo"},
		"url anchor":                            {url: baseURL + "/defs.json#named"},
		"empty fragment names the root": {
			url:     baseURL + "/defs.json#",
			valid:   "other: 1\n",
			invalid: "- x\n",
			want:    `expected "object", got "array"`,
		},
		"url pointer with a caller's ref resolver": {
			url:  baseURL + "/defs.json#/$defs/Foo",
			opts: []schema.RegistryOption{urnResolver},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, requests := newClient()
			opts := append([]schema.RegistryOption{
				schema.WithHTTPClient(client),
				schema.WithResolvers(schema.URL(tc.url)),
			}, tc.opts...)
			reg := schema.NewRegistry(opts...)

			requireAccepts(t, reg, cmp.Or(tc.valid, "name: x\n"))

			requireRejects(t, reg,
				cmp.Or(tc.invalid, "other: 1\n"),
				cmp.Or(tc.want, `missing required property "name"`))

			assert.Equal(t, int32(1), requests.Load())
		})
	}

	t.Run("file url pointer", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts []schema.RegistryOption
		}{
			"registry's ref resolver": {},
			"caller's ref resolver":   {opts: []schema.RegistryOption{urnResolver}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// Each case writes defs.json to a directory of its own,
				// and the registry sends no request for it.
				defsFile := yamltest.WriteFile(t, t.TempDir(), "defs.json", defsSchema)
				ref := fileOrURL(t, "", plainFileURL(defsFile)+"#/$defs/Foo")

				client, requests := newClient()
				opts := append([]schema.RegistryOption{
					schema.WithHTTPClient(client),
					schema.WithResolvers(ref),
				}, tc.opts...)
				reg := schema.NewRegistry(opts...)

				requireAccepts(t, reg, "name: x\n")

				requireRejects(t, reg, "other: 1\n", `missing required property "name"`)

				assert.Equal(t, int32(0), requests.Load())
			})
		}
	})

	t.Run("fragments of one document apply their own subschemas", func(t *testing.T) {
		t.Parallel()

		client, requests := newClient()
		reg := schema.NewRegistry(schema.WithHTTPClient(client))

		foo, err := reg.Schema(t.Context(), schema.URL(baseURL+"/defs.json#/$defs/Foo"))
		require.NoError(t, err)

		bar, err := reg.Schema(t.Context(), schema.URL(baseURL+"/defs.json#/$defs/Bar"))
		require.NoError(t, err)

		require.NoError(t, yamltest.FirstDocument(t, "name: x\n").Validate(t.Context(), foo))
		require.ErrorContains(t, yamltest.FirstDocument(t, "id: 1\n").Validate(t.Context(), foo),
			`missing required property "name"`)

		require.NoError(t, yamltest.FirstDocument(t, "id: 1\n").Validate(t.Context(), bar))
		require.ErrorContains(t, yamltest.FirstDocument(t, "name: x\n").Validate(t.Context(), bar),
			`missing required property "id"`)

		// The registry fetches the document once for each schema it compiles.
		assert.Equal(t, int32(2), requests.Load())
	})

	t.Run("a pointer that names nothing does not compile", func(t *testing.T) {
		t.Parallel()

		client, _ := newClient()
		reg := schema.NewRegistry(schema.WithHTTPClient(client))

		_, err := reg.Schema(t.Context(), schema.URL(baseURL+"/defs.json#/$defs/Missing"))
		require.ErrorIs(t, err, schema.ErrCompile)
	})
}

func TestRegistry_FragmentRefsDraft07(t *testing.T) {
	t.Parallel()

	// Draft-07 ignores the keywords beside a $ref, so Bar takes any
	// string. The tuple document also holds array-form items, which only
	// draft-07 accepts, and a draft-07 fragment $id that names an anchor.
	base := serve(t, map[string]string{
		"/defs.json": `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"definitions": {
				"Bar": {"$ref": "#/definitions/Str", "type": "integer"},
				"Str": {"type": "string"}
			}
		}`,
		// The first document again, written in YAML.
		"/defs.yaml": stringtest.Input(`
			$schema: http://json-schema.org/draft-07/schema#
			definitions:
			  Bar:
			    $ref: '#/definitions/Str'
			    type: integer
			  Str:
			    type: string
		`),
		"/tuple.json": `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"definitions": {
				"Str": {"type": "string"},
				"T": {"$id": "#tuple", "items": [{"type": "string"}]}
			}
		}`,
	})

	// Each subschema takes a string, so the invalid document of each case
	// holds an integer in its place.
	tcs := map[string]struct {
		path    string
		valid   string
		invalid string
	}{
		"keywords beside a $ref": {
			path:    "/defs.json#/definitions/Bar",
			valid:   "x\n",
			invalid: "5\n",
		},
		"keywords beside a $ref in a YAML document": {
			path:    "/defs.yaml#/definitions/Bar",
			valid:   "x\n",
			invalid: "5\n",
		},
		"a pointer into a document with array-form items": {
			path:    "/tuple.json#/definitions/Str",
			valid:   "x\n",
			invalid: "5\n",
		},
		"a draft-07 anchor with array-form items": {
			path:    "/tuple.json#tuple",
			valid:   "- x\n- 5\n",
			invalid: "- 5\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithResolvers(schema.URL(base + tc.path)))

			requireAccepts(t, reg, tc.valid)

			requireRejects(t, reg, tc.invalid, `expected "string", got "integer"`)
		})
	}
}

func TestRegistry_RelativeRefs(t *testing.T) {
	t.Parallel()

	const (
		mainSchema = `{"properties": {"a": {"$ref": "defs.json"}}}`
		defsSchema = `{"type": "string"}`
	)

	tmpDir := t.TempDir()
	yamltest.WriteFiles(t, tmpDir, map[string]string{"main.json": mainSchema, "defs.json": defsSchema})

	bundle := yamltest.MapFS(map[string]string{
		"schemas/main.json": mainSchema,
		"schemas/defs.json": defsSchema,
	})

	// The defs file on disk, and the directory that holds it as a base.
	defsURL := plainFileURL(filepath.Join(tmpDir, "defs.json"))
	dirURL := plainFileURL(tmpDir) + "/"

	base := serve(t, map[string]string{
		"/s/main.json": mainSchema,
		"/s/defs.json": defsSchema,
		// A schema that names the defs file on disk.
		"/s/local.json": fmt.Sprintf(`{"properties": {"a": {"$ref": %q}}}`, defsURL),
		// A schema whose $id moves its base to the directory on disk, so
		// "defs.json" names the defs file there.
		"/s/idbase.json": fmt.Sprintf(`{"$id": %q, "properties": {"a": {"$ref": "defs.json"}}}`, dirURL),
		// A schema that names the defs file on disk from an unknown
		// keyword, which a JSON pointer $ref reaches.
		"/s/keyword.json": fmt.Sprintf(
			`{"properties": {"a": {"$ref": "#/x-local"}}, "x-local": {"$ref": %q}}`,
			defsURL,
		),
		// Schemas that spell the keyword in upper case. The schema decoder
		// reads "$REF" as $ref and "$ID" as $id.
		"/s/upperref.json": fmt.Sprintf(`{"properties": {"a": {"$REF": %q}}}`, defsURL),
		"/s/upperid.json":  fmt.Sprintf(`{"$ID": %q, "properties": {"a": {"$ref": "defs.json"}}}`, dirURL),
	})

	tcs := map[string]struct {
		ref schema.Ref
	}{
		"file on disk":          {ref: schema.File(filepath.Join(tmpDir, "main.json"))},
		"file in a file system": {ref: schema.FileFS(bundle, "schemas/main.json")},
		"url":                   {ref: schema.URL(base + "/s/main.json")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithResolvers(tc.ref))

			requireAccepts(t, reg, "a: x\n")

			requireRejects(t, reg, "a: 5\n", `$.a: expected "string", got "integer"`)
		})
	}

	t.Run("a url schema reads no local file", func(t *testing.T) {
		t.Parallel()

		requireRefModes(t, "a: 5\n", "cannot resolve $ref", schema.WithResolvers(schema.URL(base+"/s/local.json")))
	})

	t.Run("a url reached from a file schema reads no local file", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			remote string
		}{
			"$ref names the file":               {remote: "/s/local.json"},
			"$id names a file base":             {remote: "/s/idbase.json"},
			"an unknown keyword names the file": {remote: "/s/keyword.json"},
			"$REF names the file":               {remote: "/s/upperref.json"},
			"$ID names a file base":             {remote: "/s/upperid.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				root := yamltest.WriteFile(t, t.TempDir(), "root.json", fmt.Sprintf(`{"$ref": %q}`, base+tc.remote))

				for name, mode := range refModes {
					t.Run(name, func(t *testing.T) {
						t.Parallel()

						reg := schema.NewRegistry(requireRefs(mode.require), schema.WithResolvers(schema.File(root)))

						err := checkInput(t, reg, "a: 5\n")
						require.ErrorIs(t, err, mode.err)
						assert.NotContains(t, err.Error(), `expected "string", got "integer"`)
						assert.Contains(t, err.Error(), "names local file")

						// A string fails too, since the registry refuses the
						// remote schema rather than skip the reference.
						err = checkInput(t, reg, "a: x\n")
						require.ErrorContains(t, err, "names local file")
					})
				}
			})
		}
	})
}

func TestRegistry_RefDocuments(t *testing.T) {
	t.Parallel()

	defsFile := yamltest.WriteFile(t, t.TempDir(), "defs.json", `{"type": "string"}`)

	defsURL := plainFileURL(defsFile)
	namesFile := fmt.Sprintf(`{"properties": {"a": {"$ref": %q}}}`, defsURL)

	served := map[string]string{
		"/a.json":          `{"properties": {"x": {"$ref": "defs.json"}}}`,
		"/b.json":          `{"properties": {"x": {"$ref": "defs.json"}}}`,
		"/defs.json":       `{"type": "string"}`,
		"/f.json":          `{"properties": {"b": {"$ref": "flaky.json"}}}`,
		"/flaky.json":      `{"type": "integer"}`,
		"/g.json":          `{"properties": {"b": {"$ref": "missing.json"}}}`,
		"/kept/f.json":     `{"properties": {"b": {"$ref": "flaky.json"}}}`,
		"/kept/flaky.json": `{"type": "integer"}`,
		"/names-file.json": namesFile,
		"/via-remote.json": `{"$ref": "remote-names-file.json"}`,
		// The remote document the two roots of one subtest share.
		"/remote-names-file.json": namesFile,
	}

	// Count the requests for each path, so a subtest can tell how often the
	// registry fetched a document. Each flaky document answers 500 to its
	// first request, and the missing one answers 404 to every request.
	hits := map[string]*atomic.Int32{"/missing.json": {}}
	for path := range served {
		hits[path] = &atomic.Int32{}
	}

	files := schemaHandler(served)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if counter, ok := hits[r.URL.Path]; ok {
			n := counter.Add(1)
			if n == 1 && strings.HasSuffix(r.URL.Path, "/flaky.json") {
				http.Error(w, "unavailable", http.StatusInternalServerError)

				return
			}
		}

		files.ServeHTTP(w, r)
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	// A subtest that waits out the retry interval runs in a synctest
	// bubble, where a fetch over a socket never lets the clock move. A
	// registry with the memory option fetches memoryURL from the handler
	// itself, so it gets the answers of the server and adds to the same
	// counts.
	const memoryURL = "http://schemas.test"

	memory := schema.WithHTTPClient(handlerTransport(handler).Client())

	t.Run("a document several schemas reference loads once", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		for _, path := range []string{"/a.json", "/b.json"} {
			s, err := reg.Schema(t.Context(), schema.URL(server.URL+path))
			require.NoError(t, err)

			err = yamltest.FirstDocument(t, "x: 5\n").Validate(t.Context(), s)
			require.ErrorContains(t, err, `expected "string"`)
		}

		assert.Equal(t, int32(1), hits["/defs.json"].Load())
	})

	t.Run("a document that missed at compile loads once after the retry interval", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			reg := schema.NewRegistry(
				memory,
				requireRefs(false),
				schema.WithResolvers(schema.URL(memoryURL+"/f.json")),
			)

			// The compile missed the document. The registry remembers the
			// miss, so a validation inside the interval fetches nothing,
			// although the document has recovered.
			for range 5 {
				err := checkInput(t, reg, "b: 2\n")
				require.ErrorIs(t, err, schema.ErrValidate)
			}

			assert.Equal(t, int32(1), hits["/flaky.json"].Load())

			time.Sleep(time.Minute)

			// The first validation after the interval loads the document
			// for every validation after.
			for range 5 {
				requireAccepts(t, reg, "b: 2\n")
			}

			assert.Equal(t, int32(2), hits["/flaky.json"].Load())

			requireRejects(t, reg, "b: x\n", `expected "integer"`)
		})
	})

	t.Run("a required document that missed at compile is fetched again after the retry interval", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ref := schema.URL(memoryURL + "/kept/f.json")
			reg := schema.NewRegistry(memory, schema.WithResolvers(ref))

			for range 3 {
				err := checkInput(t, reg, "b: 2\n")
				require.ErrorIs(t, err, schema.ErrLoad)
				assert.Contains(t, err.Error(), "/kept/flaky.json")
			}

			_, err := reg.Schema(t.Context(), ref)
			require.ErrorIs(t, err, schema.ErrLoad)

			// The registry remembers the miss in place of the schema, so
			// inside the interval it fetches neither again, although the
			// document has recovered.
			assert.Equal(t, int32(1), hits["/kept/f.json"].Load())
			assert.Equal(t, int32(1), hits["/kept/flaky.json"].Load())

			time.Sleep(time.Minute)

			// The first request after the interval fetches both and
			// compiles the schema.
			requireAccepts(t, reg, "b: 2\n")
			assert.Equal(t, int32(2), hits["/kept/f.json"].Load())
			assert.Equal(t, int32(2), hits["/kept/flaky.json"].Load())
		})
	})

	t.Run("a document that fails to load is fetched again once per retry interval", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ref := schema.URL(memoryURL + "/g.json")
			reg := schema.NewRegistry(memory, requireRefs(false), schema.WithResolvers(ref))

			_, err := reg.Schema(t.Context(), ref)
			require.NoError(t, err)

			// The compile fetched the document in the first interval, and
			// the validations of each later one fetch it once between
			// them.
			for interval := int32(1); interval <= 3; interval++ {
				for range 3 {
					err := checkInput(t, reg, "b: 2\n")
					require.ErrorIs(t, err, schema.ErrValidate)
				}

				assert.Equal(t, interval, hits["/missing.json"].Load(), "interval %d", interval)

				time.Sleep(time.Minute)
			}
		})
	})

	t.Run("a url schema still reads no local file after a file schema loaded it", func(t *testing.T) {
		t.Parallel()

		root := yamltest.WriteFile(t, t.TempDir(), "root.json", namesFile)

		for name, mode := range refModes {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(
					requireRefs(mode.require),
					schema.WithResolvers(schema.URL(server.URL+"/names-file.json")),
				)

				s, err := reg.Schema(t.Context(), schema.File(root))
				require.NoError(t, err)

				err = yamltest.FirstDocument(t, "a: 5\n").Validate(t.Context(), s)
				require.ErrorContains(t, err, `expected "string"`)

				err = checkInput(t, reg, "a: 5\n")
				require.ErrorIs(t, err, mode.err)
				assert.Contains(t, err.Error(), "cannot resolve $ref")
			})
		}
	})

	t.Run("a cached remote document still cannot name a local file", func(t *testing.T) {
		t.Parallel()

		root := yamltest.WriteFile(t, t.TempDir(), "root.json",
			fmt.Sprintf(`{"$ref": %q}`, server.URL+"/remote-names-file.json"))

		// The url root compiles only in a registry that does not require
		// refs, since it cannot reach the file its document names.
		reg := schema.NewRegistry(requireRefs(false), schema.WithResolvers(schema.File(root)))

		// A url root loads the remote document first, which may name a
		// local file there because the file stays out of its reach.
		_, err := reg.Schema(t.Context(), schema.URL(server.URL+"/via-remote.json"))
		require.NoError(t, err)
		require.Equal(t, int32(1), hits["/remote-names-file.json"].Load())

		err = checkInput(t, reg, "a: 5\n")
		require.ErrorIs(t, err, schema.ErrValidate)
		assert.NotContains(t, err.Error(), `expected "string", got "integer"`)
		assert.Contains(t, err.Error(), "names local file")

		// The file root took the document from the registry.
		assert.Equal(t, int32(1), hits["/remote-names-file.json"].Load())
	})
}

func TestRegistry_YAMLSchemas(t *testing.T) {
	t.Parallel()

	// The root takes the schema of a named document from the document
	// beside it, which reads the type of a name through a $ref of its
	// own. Each pair of files spells that in YAML, in JSON, or in both,
	// under a name of either kind.
	const defsJSON = `{
		"$defs": {
			"name": {"type": "string"},
			"named": {"properties": {"name": {"$ref": "#/$defs/name"}}}
		}
	}`

	defsYAML := stringtest.Input(`
		$defs:
		  name:
		    type: string
		  named:
		    properties:
		      name:
		        $ref: '#/$defs/name'
	`)

	// The schema of a named document, with no $ref.
	namedYAML := stringtest.Input(`
		properties:
		  name:
		    type: string
	`)

	// A refFunc builds the Ref of a case and the options of its registry.
	type refFunc func(t *testing.T) (schema.Ref, []schema.RegistryOption)

	yamlFiles := map[string]string{
		"root.yaml": "$ref: defs.yaml#/$defs/named\n",
		"defs.yaml": defsYAML,
	}

	// The ref of each case names one of files. A name with a fragment
	// goes in as a file URL, which is the form that carries one.
	onDisk := func(files map[string]string, name string) refFunc {
		return func(t *testing.T) (schema.Ref, []schema.RegistryOption) {
			t.Helper()

			dir := t.TempDir()
			yamltest.WriteFiles(t, dir, files)

			file, fragment, ok := strings.Cut(name, "#")
			path := filepath.Join(dir, file)

			if !ok {
				return schema.File(path), nil
			}

			return fileOrURL(t, "", plainFileURL(path)+"#"+fragment), nil
		}
	}

	// For each file, under puts dir in front of its name.
	under := func(dir string, files map[string]string) map[string]string {
		out := make(map[string]string, len(files))
		for file, data := range files {
			out[dir+file] = data
		}

		return out
	}

	inFS := func(files map[string]string, name string) refFunc {
		return func(*testing.T) (schema.Ref, []schema.RegistryOption) {
			return schema.FileFS(yamltest.MapFS(under("schemas/", files)), "schemas/"+name), nil
		}
	}

	// Names under .test never resolve, so a fetch that bypasses the
	// client fails.
	served := func(files map[string]string, name string) refFunc {
		return func(*testing.T) (schema.Ref, []schema.RegistryOption) {
			client, _ := memoryClient(under("/", files))

			return schema.URL("http://schemas.test/" + name), []schema.RegistryOption{schema.WithHTTPClient(client)}
		}
	}

	tcs := map[string]struct {
		ref refFunc
	}{
		"file":              {ref: onDisk(yamlFiles, "root.yaml")},
		"file url fragment": {ref: onDisk(yamlFiles, "defs.yaml#/$defs/named")},
		"file system":       {ref: inFS(yamlFiles, "root.yaml")},
		"url":               {ref: served(yamlFiles, "root.yaml")},
		"url fragment":      {ref: served(yamlFiles, "defs.yaml#/$defs/named")},
		"url with no extension": {
			ref: served(map[string]string{
				"root": "$ref: defs#/$defs/named\n",
				"defs": defsYAML,
			}, "root"),
		},
		"YAML under a JSON name": {
			ref: onDisk(map[string]string{
				"root.json": "$ref: defs.json#/$defs/named\n",
				"defs.json": defsYAML,
			}, "root.json"),
		},
		"JSON under a YAML name": {
			ref: onDisk(map[string]string{
				"root.yaml": `{"$ref": "defs.yaml#/$defs/named"}`,
				"defs.yaml": defsJSON,
			}, "root.yaml"),
		},
		"JSON root and YAML target": {
			ref: onDisk(map[string]string{
				"root.json": `{"$ref": "defs.yaml#/$defs/named"}`,
				"defs.yaml": defsYAML,
			}, "root.json"),
		},
		"YAML root and JSON target": {
			ref: onDisk(map[string]string{
				"root.yaml": "$ref: defs.json#/$defs/named\n",
				"defs.json": defsJSON,
			}, "root.yaml"),
		},
		"loadable": {
			ref: func(*testing.T) (schema.Ref, []schema.RegistryOption) {
				return loading("named", []byte(namedYAML), nil), nil
			},
		},
		"embedded": {
			ref: func(*testing.T) (schema.Ref, []schema.RegistryOption) {
				return schema.Embedded([]byte(namedYAML)), nil
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ref, opts := tc.ref(t)

			s, err := schema.NewRegistry(opts...).Schema(t.Context(), ref)
			require.NoError(t, err)

			require.NoError(t, yamltest.FirstDocument(t, "name: a\n").Validate(t.Context(), s))

			err = yamltest.FirstDocument(t, "name: 1\n").Validate(t.Context(), s)
			require.ErrorContains(t, err, `expected "string", got "integer"`)
		})
	}

	t.Run("JSON under a YAML name reads its numbers as JSON", func(t *testing.T) {
		t.Parallel()

		ref, _ := onDisk(map[string]string{"schema.yaml": `{"properties": {"v": {"const": 1e3}}}`}, "schema.yaml")(t)

		s, err := schema.NewRegistry().Schema(t.Context(), ref)
		require.NoError(t, err)

		require.NoError(t, yamltest.FirstDocument(t, "v: 1000\n").Validate(t.Context(), s))
	})

	// A document whose flow mapping never closes.
	unclosedDefs := stringtest.LinesLF("$defs:", "  named: {required: [name]")

	// The schema is at fault for each of these, and the document it
	// would validate is not.
	broken := map[string]struct {
		files map[string]string
		// The file the ref names, which may end in a fragment.
		name string
		// The path the error names.
		at string
		// Wrapped by the error of a registry that requires every $ref
		// to resolve.
		err error
	}{
		"schema that does not parse": {
			files: map[string]string{"root.yaml": "required: [name\n"},
			name:  "root.yaml",
			at:    "root.yaml",
			err:   schema.ErrCompile,
		},
		"fragment of a schema that does not parse": {
			files: map[string]string{"defs.yaml": unclosedDefs},
			name:  "defs.yaml#/$defs/named",
			at:    "defs.yaml",
			err:   schema.ErrCompile,
		},
		"$ref target that does not parse": {
			files: map[string]string{
				"root.yaml": "$ref: defs.yaml#/$defs/named\n",
				"defs.yaml": unclosedDefs,
			},
			name: "root.yaml",
			at:   "defs.yaml",
			err:  schema.ErrLoad,
		},
		"schema with two documents": {
			files: map[string]string{"root.yaml": stringtest.Input(`
				required: [name]
				---
				required: [kind]
			`)},
			name: "root.yaml",
			at:   "root.yaml",
			err:  schema.ErrCompile,
		},
	}

	for name, tc := range broken {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ref, _ := onDisk(tc.files, tc.name)(t)

			err := checkInput(t, schema.NewRegistry(schema.WithResolvers(ref)), "name: a\n")
			require.ErrorIs(t, err, tc.err)
			assert.Contains(t, err.Error(), tc.at)
			assert.Contains(t, err.Error(), "YAML decode: ")

			require.NotErrorIs(t, err, niceyaml.ErrSyntax)
			assert.False(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)
		})
	}

	t.Run("a remote YAML document cannot name a local file", func(t *testing.T) {
		t.Parallel()

		defsFile := yamltest.WriteFile(t, t.TempDir(), "defs.json", `{"type": "string"}`)

		defsURL := plainFileURL(defsFile)

		refersTo := func(ref string) string {
			return stringtest.LinesLF("properties:", "  a:", "    $ref: "+ref)
		}

		// The second document spells the scheme with an escape, which
		// only the decoded value shows.
		remote := map[string]string{
			"/names-file.yaml":   refersTo(defsURL),
			"/escapes-file.yaml": refersTo(`"\x66ile` + strings.TrimPrefix(defsURL, "file") + `"`),
		}

		base := serve(t, remote)

		for path := range remote {
			t.Run(path, func(t *testing.T) {
				t.Parallel()

				root := yamltest.WriteFile(t, t.TempDir(), "root.yaml", "$ref: "+base+path+"\n")

				requireRefModes(t, "a: 5\n", "names local file", schema.WithResolvers(schema.File(root)))
			})
		}
	})
}

func TestRegistry_Schema_RedactsPassword(t *testing.T) {
	t.Parallel()

	base := serve(t, map[string]string{
		"/bad.json":     `{"type": 5}`,
		"/defs.json":    `{"$defs": {"Foo": {}}}`,
		"/garbled.json": `not json`,
		"/d/root.json":  `{"properties": {"a": {"$ref": "../anchor.json"}}}`,
		"/anchor.json":  `{"$defs": {"a": {"$anchor": "1bad"}}}`,
		"/d/lost.json":  `{"properties": {"a": {"$ref": "../gone.json"}}}`,
	})

	withPassword := addPassword(base)

	tcs := map[string]struct {
		err error
		url string
	}{
		"a relative $ref to a document that does not load": {
			url: withPassword + "/d/lost.json",
			err: schema.ErrLoad,
		},
		"a fetch that fails": {
			url: withPassword + "/missing.json",
			err: schema.ErrLoad,
		},
		"a schema that does not compile": {
			url: withPassword + "/bad.json",
			err: schema.ErrCompile,
		},
		"a fragment that names no subschema": {
			url: withPassword + "/defs.json#/$defs/Missing",
			err: schema.ErrCompile,
		},
		"a fragment of a document that does not parse": {
			url: withPassword + "/garbled.json#/$defs/Foo",
			err: schema.ErrCompile,
		},
		"a relative $ref to a document with an invalid anchor": {
			url: withPassword + "/d/root.json",
			err: schema.ErrCompile,
		},
		"a fragment whose relative $ref names an invalid anchor": {
			url: withPassword + "/d/root.json#/properties/a",
			err: schema.ErrCompile,
		},
		"a url that does not parse": {
			url: "http://user:secret@127.0.0.1:port/s.json",
			err: schema.ErrLoad,
		},
		"a password with a slash in a url that does not parse": {
			url: "http://user:secret/x@127.0.0.1:1/s.json",
			err: schema.ErrLoad,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry()

			_, err := reg.Schema(t.Context(), schema.URL(tc.url))
			require.ErrorIs(t, err, tc.err)
			assert.NotContains(t, err.Error(), "secret")
			assert.True(t, strings.HasPrefix(err.Error(), tc.err.Error()+`: "http://user:xxxxx@`), err.Error())
		})
	}
}

func TestRegistry_Check_RedactsPassword(t *testing.T) {
	t.Parallel()

	// The referenced document answers 500 to its first request, so the
	// compile misses it. The first validation after the retry interval
	// loads it, and it then holds an anchor the compiler rejects. The
	// bubble's clock passes the interval without waiting in real time, and
	// the handler answers from memory, since a fetch over a socket never
	// lets that clock move.
	synctest.Test(t, func(t *testing.T) {
		var lateHits atomic.Int32

		files := schemaHandler(map[string]string{
			"/d/root.json": `{"properties": {"a": {"$ref": "../late.json"}}}`,
			"/late.json":   `{"$defs": {"a": {"$anchor": "1bad"}}}`,
		})

		client := handlerTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/late.json" && lateHits.Add(1) == 1 {
				http.Error(w, "unavailable", http.StatusInternalServerError)

				return
			}

			files.ServeHTTP(w, r)
		})).Client()

		ref := schema.URL(addPassword("http://schemas.test") + "/d/root.json")
		reg := schema.NewRegistry(schema.WithHTTPClient(client), requireRefs(false), schema.WithResolvers(ref))

		_, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		time.Sleep(time.Minute)

		err = checkInput(t, reg, "a: 5\n")
		require.ErrorIs(t, err, schema.ErrValidate)
		assert.Contains(t, err.Error(), "1bad")
		assert.NotContains(t, err.Error(), "secret")
	})
}

func TestRegistry_RefCredentials(t *testing.T) {
	t.Parallel()

	// The server answers only a request that carries the password, so a
	// referenced document loads only when the registry sends it.
	files := schemaHandler(map[string]string{
		"/d/root.json": `{"properties": {"a": {"$ref": "../defs.json#/$defs/Str"}}}`,
		"/defs.json":   `{"$defs": {"Str": {"type": "string"}}}`,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "user" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	withPassword := addPassword(server.URL)

	tcs := map[string]struct {
		url   string
		input string
	}{
		"a whole document": {
			url:   withPassword + "/d/root.json",
			input: "a: 5\n",
		},
		"a fragment": {
			url:   withPassword + "/d/root.json#/properties/a",
			input: "5\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithResolvers(schema.URL(tc.url)))

			requireRejects(t, reg, tc.input, `expected "string", got "integer"`)
		})
	}

	t.Run("a document on another host gets no userinfo", func(t *testing.T) {
		t.Parallel()

		var sawAuth atomic.Bool

		defs := schemaHandler(map[string]string{"/defs.json": `{"type": "string"}`})

		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, _, ok := r.BasicAuth(); ok {
				sawAuth.Store(true)
			}

			defs.ServeHTTP(w, r)
		}))
		t.Cleanup(other.Close)

		root := serve(t, map[string]string{
			"/root.json": fmt.Sprintf(`{"properties": {"a": {"$ref": %q}}}`, other.URL+"/defs.json"),
		})

		ref := schema.URL(addPassword(root) + "/root.json")
		reg := schema.NewRegistry(schema.WithResolvers(ref))

		requireRejects(t, reg, "a: 5\n", `expected "string", got "integer"`)
		assert.False(t, sawAuth.Load())
	})
}

func TestRegistry_AccessNotSet(t *testing.T) {
	t.Parallel()

	// The schema rejects the body "name: 5", so a registry that loads the
	// schema reports a violation in place of the refusal.
	const (
		stringName = `{"properties": {"name": {"type": "string"}}}`
		violation  = `expected "string", got "integer"`
		schemaURL  = "https://schemas.test/s.json"
	)

	dir := t.TempDir()
	schemaPath := yamltest.WriteFile(t, dir, "schema.json", stringName)
	app := filepath.Join(dir, "app.yaml")

	bundle := yamltest.MapFS(map[string]string{"configs/schema.json": stringName})
	inBundle := []niceyaml.SourceOption{niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(bundle)}

	// Each request gets the schema, so only the refusal keeps a registry
	// from loading the schema that a URL names.
	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		//nolint:errcheck // Test helper.
		w.Write([]byte(stringName))
	})

	requireRefused := func(t *testing.T, err error) {
		t.Helper()

		requireLoadError(t, err, schema.ErrAccessNotSet)
		require.NotErrorIs(t, err, schema.ErrFileNotAllowed)
		require.NotErrorIs(t, err, schema.ErrURLNotAllowed)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
		assert.False(t, niceyaml.IsInvalid(err))
	}

	t.Run("a directive is refused before any read or fetch", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The reference of the directive.
			ref string
			// The name of the schema in the message.
			want   string
			source []niceyaml.SourceOption
		}{
			"URL": {
				ref:  schemaURL,
				want: schemaURL,
			},
			"file beside a document on disk": {
				ref:    "./schema.json",
				want:   plainFileURL(schemaPath),
				source: []niceyaml.SourceOption{niceyaml.WithFilePath(app)},
			},
			"absolute path": {
				ref:  schemaPath,
				want: plainFileURL(schemaPath),
			},
			"file beside a document in a file system": {
				ref:    "./schema.json",
				want:   "configs/schema.json",
				source: inBundle,
			},
			"URL beside a document in a file system": {
				ref:    schemaURL,
				want:   schemaURL,
				source: inBundle,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, requests := recordingClient(served)
				reg := schema.NewRegistry(
					schema.WithHTTPClient(client),
					schema.WithResolvers(schema.Directive()),
				)

				doc := yamltest.FirstDocument(t, yamltest.SchemaDirective(tc.ref, "name: 5\n"),
					append([]niceyaml.SourceOption{niceyaml.WithName("pr.yaml")}, tc.source...)...)

				for _, err := range []error{
					reg.Check(t.Context(), doc),
					doc.Validate(t.Context(), reg),
				} {
					requireRefused(t, err)
					assert.Equal(t, `pr.yaml:1:1: load schema: "`+tc.want+
						`": registry needs schema.WithAccess to load a schema that a document names`, err.Error())

					// The error binds at the comment that holds the directive.
					at, ok := yamltest.RequireSourceError(t, err).Position()
					require.True(t, ok)
					assert.Equal(t, position.New(0, 0), at)
				}

				_, err := reg.Lookup(t.Context(), doc)
				requireRefused(t, err)

				assert.Empty(t, requests())
			})
		}
	})

	t.Run("an Access loads the schema that a directive names", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			access schema.RegistryOption
			ref    string
			source []niceyaml.SourceOption
		}{
			"Unrestricted() reads a file on disk": {
				access: schema.WithAccess(schema.Unrestricted()),
				ref:    "./schema.json",
				source: []niceyaml.SourceOption{niceyaml.WithFilePath(app)},
			},
			"Unrestricted() fetches a URL": {
				access: schema.WithAccess(schema.Unrestricted()),
				ref:    schemaURL,
			},
			"AllowRoot reads a file under the root": {
				access: withAccess(t, allowRoot(t, dir)),
				ref:    "./schema.json",
				source: []niceyaml.SourceOption{niceyaml.WithFilePath(app)},
			},
			"NoAccess() reads a file in the file system of the document": {
				access: schema.WithAccess(schema.NoAccess()),
				ref:    "./schema.json",
				source: inBundle,
			},
			"the zero Access reads a file in the file system of the document": {
				access: schema.WithAccess(&schema.Access{}),
				ref:    "./schema.json",
				source: inBundle,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, _ := recordingClient(served)
				reg := schema.NewRegistry(
					tc.access,
					schema.WithHTTPClient(client),
					schema.WithResolvers(schema.Directive()),
				)

				doc := yamltest.FirstDocument(t, yamltest.SchemaDirective(tc.ref, "name: 5\n"), tc.source...)

				err := reg.Check(t.Context(), doc)
				require.ErrorIs(t, err, schema.ErrViolation)
				require.ErrorContains(t, err, violation)
			})
		}
	})

	t.Run("the schemas of the program load without an Access", func(t *testing.T) {
		t.Parallel()

		// The resolver reads the reference from the document and names it
		// through FileOrURL, which does not mark its Ref.
		fromField := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			var ref string

			_, err := doc.DecodeIfPresent(ctx, paths.Doc().Child("schema"), &ref)
			if err != nil {
				return schema.Ref{}, fmt.Errorf("read schema field: %w", err)
			}

			return schema.FileOrURL("", ref)
		})

		tcs := map[string]struct {
			resolver schema.Resolver
			input    string
		}{
			"File":      {resolver: schema.File(schemaPath), input: "name: 5\n"},
			"URL":       {resolver: schema.URL(schemaURL), input: "name: 5\n"},
			"FileFS":    {resolver: schema.FileFS(bundle, "configs/schema.json"), input: "name: 5\n"},
			"Embedded":  {resolver: schema.Embedded([]byte(stringName)), input: "name: 5\n"},
			"FileOrURL": {resolver: fromField, input: "schema: " + schemaPath + "\nname: 5\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, _ := recordingClient(served)

				// The directive resolver comes first and declines the
				// document, which has no directive.
				reg := schema.NewRegistry(
					schema.WithHTTPClient(client),
					schema.WithResolvers(schema.Directive(), tc.resolver),
				)

				requireRejects(t, reg, tc.input, violation)
			})
		}
	})

	t.Run("a directive of none resolves without an Access", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive(), schema.File(schemaPath)))

		requireAccepts(t, reg, yamltest.SchemaDirective("none", "name: 5\n"))
	})

	t.Run("a Ref from RefBeside is refused without a directive", func(t *testing.T) {
		t.Parallel()

		beside := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			return schema.RefBeside(doc, "schema.json")
		})

		reg := schema.NewRegistry(schema.WithResolvers(beside))
		doc := yamltest.FirstDocumentWithPath(t, "name: 5\n", app)

		err := reg.Check(t.Context(), doc)
		requireRefused(t, err)

		_, ok := yamltest.RequireSourceError(t, err).Position()
		assert.False(t, ok)

		allowed := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(beside))

		err = allowed.Check(t.Context(), doc)
		require.ErrorContains(t, err, violation)
	})

	t.Run("the refusal stays out of the cache", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, "name: 5\n", app)

		named, err := schema.RefBeside(doc, "./schema.json")
		require.NoError(t, err)

		own := schema.File(schemaPath)
		require.Equal(t, own.Key(), named.Key())

		reg := schema.NewRegistry()

		_, err = reg.Schema(t.Context(), named)
		requireRefused(t, err)
		requireLoadErrorOf(t, err, named, schema.ErrLoad)

		// The refusal left no failure under the key, so the Ref of the
		// program loads.
		compiled, err := reg.Schema(t.Context(), own)
		require.NoError(t, err)
		require.NotNil(t, compiled)

		// The schema that the Ref of the program cached does not answer
		// the Ref of the document.
		_, err = reg.Schema(t.Context(), named)
		requireRefused(t, err)
	})

	t.Run("Load refuses the Ref of a document", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, "name: 5\n", app)

		named, err := schema.RefBeside(doc, "./schema.json")
		require.NoError(t, err)

		_, err = schema.NewRegistry().Load(t.Context(), named)
		requireRefused(t, err)
		requireLoadErrorOf(t, err, named, schema.ErrLoad)

		data, err := schema.NewRegistry(schema.WithAccess(schema.Unrestricted())).Load(t.Context(), named)
		require.NoError(t, err)
		assert.JSONEq(t, stringName, string(data))
	})

	t.Run("the registry that loads the schema needs the Access", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, yamltest.SchemaDirective("./schema.json", "name: 5\n"), app)

		// A registry that is a resolver hands over a compiled schema, so
		// the registry that asked does not load anything.
		inner := schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(schema.Directive()))
		outer := schema.NewRegistry(schema.WithResolvers(inner))

		err := outer.Check(t.Context(), doc)
		require.ErrorContains(t, err, violation)

		// The Access of the registry that asked does not reach the
		// registry that loads.
		inner = schema.NewRegistry(schema.WithResolvers(schema.Directive()))
		outer = schema.NewRegistry(schema.WithAccess(schema.Unrestricted()), schema.WithResolvers(inner))

		err = outer.Check(t.Context(), doc)
		requireRefused(t, err)
	})
}
