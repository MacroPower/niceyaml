package schema_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// openRoot opens an [os.Root] on dir and closes it when the test ends.
func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()

	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })

	return root
}

// allowRoot returns [schema.AllowRoot] of a root on dir that closes when
// the test ends.
func allowRoot(t *testing.T, dir string) schema.AccessOption {
	t.Helper()

	return schema.AllowRoot(openRoot(t, dir))
}

// withAccess returns [schema.WithAccess] of the access opts allow, and
// fails the test when [schema.NewAccess] returns an error.
func withAccess(t *testing.T, opts ...schema.AccessOption) schema.RegistryOption {
	t.Helper()

	access, err := schema.NewAccess(opts...)
	require.NoError(t, err)

	return schema.WithAccess(access)
}

func TestNewAccess(t *testing.T) {
	t.Parallel()

	t.Run("without options it reads no file and fetches no URL", func(t *testing.T) {
		t.Parallel()

		path := yamltest.WriteFile(t, t.TempDir(), "s.json", objectSchema)

		none, err := schema.NewAccess()
		require.NoError(t, err)

		tcs := map[string]struct {
			access *schema.Access
		}{
			"no options":  {access: none},
			"zero Access": {access: &schema.Access{}},
			"nil Access":  {},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, requests := recordingClient(anySchema)
				reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithAccess(tc.access))

				_, err := reg.Load(t.Context(), schema.File(path))
				requireLoadError(t, err, fs.ErrInvalid)
				require.ErrorContains(
					t,
					err,
					"read "+path+": invalid argument: the access of the registry allows no file",
				)

				_, err = reg.Load(t.Context(), schema.URL("https://schemas.test/s.json"))
				requireLoadError(t, err, schema.ErrURLNotAllowed)
				assert.False(t, niceyaml.IsInvalid(err))
				assert.Empty(t, requests())
			})
		}
	})

	t.Run("an option that does not hold is an error", func(t *testing.T) {
		t.Parallel()

		closed, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		require.NoError(t, closed.Close())

		tcs := map[string]struct {
			opts []schema.AccessOption
			want []string
		}{
			"nil root": {
				opts: []schema.AccessOption{schema.AllowRoot(nil)},
				want: []string{"schema.AllowRoot: root is nil"},
			},
			"second root": {
				opts: []schema.AccessOption{allowRoot(t, t.TempDir()), allowRoot(t, t.TempDir())},
				want: []string{"schema.AllowRoot: ", "the access holds a root already"},
			},
			"closed root": {
				opts: []schema.AccessOption{schema.AllowRoot(closed)},
				want: []string{"schema.AllowRoot: root " + closed.Name()},
			},
			"invalid prefix": {
				opts: []schema.AccessOption{schema.AllowURLPrefixes("ftp://schemas.test/")},
				want: []string{`schema.AllowURLPrefixes: "ftp://schemas.test/": prefix is not an http or https URL`},
			},
			"root beside every file": {
				opts: []schema.AccessOption{schema.AllowAnyFile(), allowRoot(t, t.TempDir())},
				want: []string{"schema.AllowRoot: ", "the access allows every file already"},
			},
			"every file beside a root": {
				opts: []schema.AccessOption{allowRoot(t, t.TempDir()), schema.AllowAnyFile()},
				want: []string{"schema.AllowAnyFile: the access holds a root already"},
			},
			"prefixes beside every URL": {
				opts: []schema.AccessOption{schema.AllowAnyURL(), schema.AllowURLPrefixes("https://schemas.test/")},
				want: []string{"schema.AllowURLPrefixes: the access allows every URL already"},
			},
			"every URL beside prefixes": {
				opts: []schema.AccessOption{schema.AllowURLPrefixes("https://schemas.test/"), schema.AllowAnyURL()},
				want: []string{"schema.AllowAnyURL: the access holds URL prefixes already"},
			},
			"each option that does not hold": {
				opts: []schema.AccessOption{
					schema.AllowRoot(nil),
					schema.AllowURLPrefixes("https://schemas.test/"),
					schema.AllowURLPrefixes("ftp://schemas.test/"),
				},
				want: []string{
					"schema.AllowRoot: root is nil",
					`schema.AllowURLPrefixes: "ftp://schemas.test/"`,
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				access, err := schema.NewAccess(tc.opts...)
				require.Error(t, err)
				assert.Nil(t, access)

				for _, want := range tc.want {
					require.ErrorContains(t, err, want)
				}
			})
		}
	})
}

func TestUnrestricted(t *testing.T) {
	t.Parallel()

	path := yamltest.WriteFile(t, t.TempDir(), "s.json", objectSchema)

	tcs := map[string]struct {
		opts []schema.RegistryOption
	}{
		"WithAccess(Unrestricted)": {opts: []schema.RegistryOption{schema.WithAccess(schema.Unrestricted)}},
		"no WithAccess":            {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, requests := recordingClient(anySchema)
			reg := schema.NewRegistry(append(tc.opts, schema.WithHTTPClient(client))...)

			data, err := reg.Load(t.Context(), schema.File(path))
			require.NoError(t, err)
			assert.JSONEq(t, objectSchema, string(data))

			_, err = reg.Schema(t.Context(), schema.URL("https://evil.test/app.json"))
			require.NoError(t, err)
			assert.Equal(t, []string{"https://evil.test/app.json"}, requests())
		})
	}
}

func TestAllowAnyFile(t *testing.T) {
	t.Parallel()

	path := yamltest.WriteFile(t, t.TempDir(), "s.json", objectSchema)

	client, requests := recordingClient(anySchema)
	reg := schema.NewRegistry(
		schema.WithHTTPClient(client),
		withAccess(t, schema.AllowAnyFile(), schema.AllowURLPrefixes("https://schemas.test/")),
	)

	data, err := reg.Load(t.Context(), schema.File(path))
	require.NoError(t, err)
	assert.JSONEq(t, objectSchema, string(data))

	_, err = reg.Schema(t.Context(), schema.URL("https://schemas.test/s.json"))
	require.NoError(t, err)

	_, err = reg.Schema(t.Context(), schema.URL("https://evil.test/s.json"))
	requireLoadError(t, err, schema.ErrURLNotAllowed)
	assert.Equal(t, []string{"https://schemas.test/s.json"}, requests())
}

func TestAllowAnyURL(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	tenant := filepath.Join(base, "tenant")
	yamltest.WriteFiles(t, base, map[string]string{
		"tenant/s.json": objectSchema,
		"shared/s.json": objectSchema,
	})

	tcs := map[string]struct {
		opts []schema.AccessOption
		// The error of a load of the file under the tenant, and of one
		// outside it.
		inErr, outErr error
	}{
		"alone": {
			opts:   []schema.AccessOption{schema.AllowAnyURL()},
			inErr:  fs.ErrInvalid,
			outErr: fs.ErrInvalid,
		},
		"beside a root": {
			opts:   []schema.AccessOption{schema.AllowAnyURL(), allowRoot(t, tenant)},
			outErr: fs.ErrInvalid,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, requests := recordingClient(anySchema)
			reg := schema.NewRegistry(schema.WithHTTPClient(client), withAccess(t, tc.opts...))

			_, err := reg.Schema(t.Context(), schema.URL("https://evil.test/s.json"))
			require.NoError(t, err)
			assert.Equal(t, []string{"https://evil.test/s.json"}, requests())

			for path, want := range map[string]error{
				filepath.Join(tenant, "s.json"):         tc.inErr,
				filepath.Join(base, "shared", "s.json"): tc.outErr,
			} {
				_, err := reg.Load(t.Context(), schema.File(path))
				if want == nil {
					require.NoError(t, err, path)

					continue
				}

				requireLoadError(t, err, want)
			}
		})
	}
}

func TestWithAccess(t *testing.T) {
	t.Parallel()

	path := yamltest.WriteFile(t, t.TempDir(), "s.json", objectSchema)

	t.Run("the last option wins", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(withAccess(t), schema.WithAccess(schema.Unrestricted))

		_, err := reg.Load(t.Context(), schema.File(path))
		require.NoError(t, err)

		reg = schema.NewRegistry(schema.WithAccess(schema.Unrestricted), withAccess(t))

		_, err = reg.Load(t.Context(), schema.File(path))
		requireLoadError(t, err, fs.ErrInvalid)
	})

	t.Run("keeps its own copy of the access", func(t *testing.T) {
		t.Parallel()

		access, err := schema.NewAccess(schema.AllowURLPrefixes("https://schemas.test/"))
		require.NoError(t, err)

		opt := schema.WithAccess(access)
		*access = *schema.Unrestricted

		client, requests := recordingClient(anySchema)
		reg := schema.NewRegistry(schema.WithHTTPClient(client), opt)

		_, err = reg.Schema(t.Context(), schema.URL("https://evil.test/app.json"))
		requireLoadError(t, err, schema.ErrURLNotAllowed)
		assert.Empty(t, requests())
	})

	t.Run("file systems and bytes stay outside the access", func(t *testing.T) {
		t.Parallel()

		bundle := yamltest.MapFS(map[string]string{
			"app.yaml":    yamltest.SchemaDirective("./schema.json", "name: 5\n"),
			"schema.json": `{"properties": {"name": {"type": "string"}}}`,
		})

		reg := schema.NewRegistry(withAccess(t), schema.WithResolvers(schema.Directive()))

		_, err := reg.Schema(t.Context(), schema.FileFS(bundle, "schema.json"))
		require.NoError(t, err)

		_, err = reg.Schema(t.Context(), schema.Embedded([]byte(objectSchema)))
		require.NoError(t, err)

		source, err := niceyaml.NewSourceFromFS(bundle, "app.yaml")
		require.NoError(t, err)

		err = source.ValidateDocuments(t.Context(), reg)
		require.ErrorContains(t, err, `expected "string", got "integer"`)
	})
}

func TestAllowRoot(t *testing.T) {
	t.Parallel()

	const nameSchema = `{"properties": {"name": {"type": "string"}}}`

	t.Run("a document opened by absolute path resolves its directive", func(t *testing.T) {
		t.Parallel()

		tenant := t.TempDir()
		yamltest.WriteFiles(t, tenant, map[string]string{
			"app.yaml":    yamltest.SchemaDirective("./schema.json", "name: 5\n"),
			"ok.yaml":     yamltest.SchemaDirective("./schema.json", "name: cafe\n"),
			"schema.json": nameSchema,
		})

		reg := schema.NewRegistry(
			withAccess(t, allowRoot(t, tenant)),
			schema.WithResolvers(schema.Directive()),
		)

		source, err := niceyaml.NewSourceFromFile(filepath.Join(tenant, "app.yaml"))
		require.NoError(t, err)

		err = source.ValidateDocuments(t.Context(), reg)
		require.ErrorContains(t, err, `expected "string", got "integer"`)

		source, err = niceyaml.NewSourceFromFile(filepath.Join(tenant, "ok.yaml"))
		require.NoError(t, err)
		require.NoError(t, source.ValidateDocuments(t.Context(), reg))
	})

	t.Run("a path the directory does not hold names no file", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		tenant := filepath.Join(base, "tenant")
		yamltest.WriteFiles(t, base, map[string]string{
			"tenant/schema.json": nameSchema,
			"shared/schema.json": nameSchema,
		})

		missing := filepath.Join(tenant, "schemas", "missing.json")

		tcs := map[string]struct {
			path string
			err  error
			want string
		}{
			"path in another directory": {
				path: filepath.Join(base, "shared", "schema.json"),
				err:  fs.ErrInvalid,
				want: "not under " + tenant,
			},
			"path through the parent": {
				path: filepath.Join(tenant, "..", "shared", "schema.json"),
				err:  fs.ErrInvalid,
				want: "not under " + tenant,
			},
			"the directory itself": {
				path: tenant,
				err:  fs.ErrInvalid,
				want: "not a regular file",
			},
			"drive-letter path": {
				path: "Q:/schemas/config.json",
				err:  fs.ErrInvalid,
			},
			// The error reports the path on disk.
			"missing file": {
				path: missing,
				err:  fs.ErrNotExist,
				want: "read " + missing,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(withAccess(t, allowRoot(t, tenant)))

				_, err := reg.Load(t.Context(), schema.File(tc.path))
				requireLoadError(t, err, tc.err)
				require.ErrorContains(t, err, tc.want)
			})
		}
	})

	t.Run("a $ref reads under the directory and no further", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		tenant := filepath.Join(base, "tenant")
		yamltest.WriteFiles(t, base, map[string]string{
			"tenant/in.json":   `{"properties": {"in": {"$ref": "defs.json"}}}`,
			"tenant/out.json":  `{"properties": {"out": {"$ref": "../shared/defs.json"}}}`,
			"tenant/defs.json": `{"type": "string"}`,
			"shared/defs.json": `{"type": "string"}`,
		})

		access := withAccess(t, allowRoot(t, tenant))

		reg := schema.NewRegistry(access, schema.WithResolvers(schema.File(filepath.Join(tenant, "in.json"))))

		requireRejects(t, reg, "in: 5\n", `$.in: expected "string", got "integer"`)

		requireRefModes(t, "out: 5\n", "not under "+tenant,
			access,
			schema.WithResolvers(schema.File(filepath.Join(tenant, "out.json"))),
		)

		// Under Unrestricted, the same schema reads the file outside.
		disk := schema.NewRegistry(schema.WithResolvers(schema.File(filepath.Join(tenant, "out.json"))))
		requireRejects(t, disk, "out: 5\n", `$.out: expected "string", got "integer"`)
	})

	t.Run("a fragment selects a subschema", func(t *testing.T) {
		t.Parallel()

		tenant := t.TempDir()
		yamltest.WriteFiles(t, tenant, map[string]string{
			"defs.json": `{"$defs": {"Foo": {"$ref": "#/$defs/Name"}, "Name": {"required": ["name"]}}}`,
		})

		ref := fileOrURL(t, "", plainFileURL(filepath.Join(tenant, "defs.json"))+"#/$defs/Foo")
		reg := schema.NewRegistry(withAccess(t, allowRoot(t, tenant)), schema.WithResolvers(ref))

		requireAccepts(t, reg, "name: x\n")

		requireRejects(t, reg, "other: 1\n", `missing required property "name"`)
	})

	t.Run("a link to the directory names the directory", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		dir := filepath.Join(base, "dir")
		link := filepath.Join(base, "link")

		yamltest.WriteFile(t, dir, "schemas/s.json", nameSchema)

		yamltest.SymlinkOrSkip(t, dir, link)

		tcs := map[string]struct {
			at   string
			path string
			err  error
		}{
			"path through the link": {
				at:   dir,
				path: filepath.Join(link, "schemas", "s.json"),
			},
			"directory through the link": {
				at:   link,
				path: filepath.Join(dir, "schemas", "s.json"),
			},
			"missing file through the link": {
				at:   dir,
				path: filepath.Join(link, "schemas", "missing.json"),
				err:  fs.ErrNotExist,
			},
			"missing directory through the link": {
				at:   dir,
				path: filepath.Join(link, "missing", "s.json"),
				err:  fs.ErrNotExist,
			},
			"path beside the link": {
				at:   dir,
				path: filepath.Join(base, "schemas", "s.json"),
				err:  fs.ErrInvalid,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(withAccess(t, allowRoot(t, tc.at)))

				data, err := reg.Load(t.Context(), schema.File(tc.path))
				if tc.err != nil {
					requireLoadError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.JSONEq(t, nameSchema, string(data))
			})
		}
	})

	t.Run("a link inside the directory", func(t *testing.T) {
		t.Parallel()

		outside := yamltest.WriteFile(t, t.TempDir(), "s.json", nameSchema)

		tcs := map[string]struct {
			// The target of the link at s.json in the root.
			target string
			err    error
		}{
			"to a file outside": {target: outside, err: schema.ErrLoad},
			"to a file inside":  {target: "real.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				yamltest.WriteFile(t, dir, "real.json", nameSchema)
				yamltest.SymlinkOrSkip(t, tc.target, filepath.Join(dir, "s.json"))

				reg := schema.NewRegistry(withAccess(t, allowRoot(t, dir)))

				data, err := reg.Load(t.Context(), schema.File(filepath.Join(dir, "s.json")))
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.JSONEq(t, nameSchema, string(data))
			})
		}
	})
}

// A relative document resolves against the working directory, as it does
// under Unrestricted, and not against the directory of the root. The test
// changes the process's working directory, so it does not run in
// parallel.
//
//nolint:paralleltest // See above.
func TestAllowRoot_RelativeDocument(t *testing.T) {
	repo := t.TempDir()

	// The schema beside the document takes only a string, and the one at
	// the root of the repository takes anything.
	yamltest.WriteFiles(t, repo, map[string]string{
		"schema.json":     `{}`,
		"sub/schema.json": `{"properties": {"name": {"type": "string"}}}`,
		"sub/app.yaml":    yamltest.SchemaDirective("./schema.json", "name: 5\n"),
	})

	t.Chdir(filepath.Join(repo, "sub"))

	reg := schema.NewRegistry(
		withAccess(t, allowRoot(t, repo)),
		schema.WithResolvers(schema.Directive()),
	)

	source, err := niceyaml.NewSourceFromFile("app.yaml")
	require.NoError(t, err)

	err = source.ValidateDocuments(t.Context(), reg)
	require.ErrorContains(t, err, `expected "string", got "integer"`)
}

// A relative name of a root resolves against the working directory when
// NewAccess runs. NewAccess refuses a name that then names a directory
// other than the one the root holds. The test changes the process's
// working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestAllowRoot_RelativeName(t *testing.T) {
	before, at, after := t.TempDir(), t.TempDir(), t.TempDir()

	for _, dir := range []string{before, at, after} {
		yamltest.WriteFile(t, dir, "s.json", `{"type": "object"}`)
	}

	//nolint:paralleltest // See above.
	t.Run("the name resolves when NewAccess runs", func(t *testing.T) {
		t.Chdir(at)

		opt := allowRoot(t, ".")
		access, err := schema.NewAccess(opt)
		require.NoError(t, err)

		t.Chdir(after)

		reg := schema.NewRegistry(schema.WithAccess(access))

		data, err := reg.Load(t.Context(), schema.File(filepath.Join(at, "s.json")))
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))

		for _, dir := range []string{before, after} {
			_, err := reg.Load(t.Context(), schema.File(filepath.Join(dir, "s.json")))
			require.ErrorIs(t, err, fs.ErrInvalid, dir)
		}
	})

	//nolint:paralleltest // See above.
	t.Run("a name that names another directory is an error", func(t *testing.T) {
		t.Chdir(before)

		opt := allowRoot(t, ".")

		t.Chdir(at)

		access, err := schema.NewAccess(opt)
		require.ErrorContains(t, err, "names a directory other than the one the root holds")
		assert.Nil(t, access)
	})
}

// A shell that enters a project through a symbolic link leaves the link
// in the working directory. A program that finds the project root itself
// names the directory the link leads to, and a relative document still
// lies under that root. The test changes the process's working
// directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestAllowRoot_WorkingDirectoryThroughLink(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "link")

	yamltest.WriteFiles(t, base, map[string]string{
		"repo/sub/schema.json": `{"properties": {"name": {"type": "string"}}}`,
		"repo/sub/app.yaml":    yamltest.SchemaDirective("./schema.json", "name: 5\n"),
	})

	// The temporary directory might lie behind a link of its own.
	repo, err := filepath.EvalSymlinks(filepath.Join(base, "repo"))
	require.NoError(t, err)

	yamltest.SymlinkOrSkip(t, repo, link)

	t.Chdir(filepath.Join(link, "sub"))

	wd, err := os.Getwd()
	require.NoError(t, err)

	if wd != filepath.Join(link, "sub") {
		t.Skipf("the platform resolves the link in the working directory: %s", wd)
	}

	reg := schema.NewRegistry(
		withAccess(t, allowRoot(t, repo)),
		schema.WithResolvers(schema.Directive()),
	)

	source, err := niceyaml.NewSourceFromFile("app.yaml")
	require.NoError(t, err)

	err = source.ValidateDocuments(t.Context(), reg)
	require.ErrorContains(t, err, `expected "string", got "integer"`)
}
