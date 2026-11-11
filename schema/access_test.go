package schema_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
			"NoAccess":    {access: schema.NoAccess},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, requests := recordingClient(anySchema)
				reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithAccess(tc.access))

				_, err := reg.Load(t.Context(), schema.File(path))
				requireLoadError(t, err, schema.ErrFileNotAllowed)
				require.NotErrorIs(t, err, fs.ErrInvalid)
				require.ErrorContains(
					t,
					err,
					"read "+path+": file not allowed: the access of the registry allows no file",
				)
				assert.False(t, niceyaml.IsInvalid(err))

				_, err = reg.Load(t.Context(), schema.URL("https://schemas.test/s.json"))
				requireLoadError(t, err, schema.ErrURLNotAllowed)
				assert.False(t, niceyaml.IsInvalid(err))
				assert.Empty(t, requests())
			})
		}
	})

	t.Run("WithAccess panics on a nil Access", func(t *testing.T) {
		t.Parallel()

		// A nil Access comes only from an ignored error of NewAccess, so
		// the option panics rather than pick a default on a security
		// boundary.
		assert.PanicsWithValue(t, "schema.WithAccess: access is nil", func() {
			schema.WithAccess(nil)
		})
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
			inErr:  schema.ErrFileNotAllowed,
			outErr: schema.ErrFileNotAllowed,
		},
		"beside a root": {
			opts:   []schema.AccessOption{schema.AllowAnyURL(), allowRoot(t, tenant)},
			outErr: schema.ErrFileNotAllowed,
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
		requireLoadError(t, err, schema.ErrFileNotAllowed)
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

		// A refusal names the directory of the root and matches
		// ErrFileNotAllowed alone. A path under the directory that does not
		// name a regular file is invalid, not refused.
		tcs := map[string]struct {
			path string
			err  error
			// The sentinel that err rules out.
			not  error
			want string
		}{
			"path in another directory": {
				path: filepath.Join(base, "shared", "schema.json"),
				err:  schema.ErrFileNotAllowed,
				not:  fs.ErrInvalid,
				want: "file not allowed: not under " + tenant,
			},
			"path through the parent": {
				path: filepath.Join(tenant, "..", "shared", "schema.json"),
				err:  schema.ErrFileNotAllowed,
				not:  fs.ErrInvalid,
				want: "file not allowed: not under " + tenant,
			},
			"the directory itself": {
				path: tenant,
				err:  fs.ErrInvalid,
				not:  schema.ErrFileNotAllowed,
				want: "not a regular file",
			},
			"drive-letter path": {
				path: "Q:/schemas/config.json",
				err:  schema.ErrFileNotAllowed,
				not:  fs.ErrInvalid,
				// Off Windows, the drive letter names no file under the
				// directory. On Windows, the drive lies outside it.
				want: "file not allowed",
			},
			// The error reports the path on disk.
			"missing file": {
				path: missing,
				err:  fs.ErrNotExist,
				not:  schema.ErrFileNotAllowed,
				want: "read " + missing,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(withAccess(t, allowRoot(t, tenant)))

				_, err := reg.Load(t.Context(), schema.File(tc.path))
				requireLoadError(t, err, tc.err)
				require.NotErrorIs(t, err, tc.not)
				require.ErrorContains(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(err))
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

	t.Run("several roots allow the files under each directory", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		tenant := filepath.Join(base, "tenant")
		shared := filepath.Join(base, "shared")
		yamltest.WriteFiles(t, base, map[string]string{
			"tenant/app.yaml": yamltest.SchemaDirective("./t.json", "name: 5\n"),
			"tenant/other.yaml": yamltest.SchemaDirective(
				filepath.Join(shared, "s.json"), "name: 5\n",
			),
			"tenant/t.json": `{"properties": {"name": {"$ref": "` + plainFileURL(
				filepath.Join(shared, "defs.json"),
			) + `"}}}`,
			"shared/defs.json": `{"type": "string"}`,
			"shared/s.json":    nameSchema,
			"other/s.json":     nameSchema,
		})

		reg := schema.NewRegistry(
			withAccess(t, allowRoot(t, tenant), allowRoot(t, shared)),
			schema.WithResolvers(schema.Directive()),
		)

		// The schema reads through the root of tenant, and its $ref reads
		// through the root of shared.
		for _, name := range []string{"app.yaml", "other.yaml"} {
			source, err := niceyaml.NewSourceFromFile(filepath.Join(tenant, name))
			require.NoError(t, err)

			err = source.ValidateDocuments(t.Context(), reg)
			require.ErrorContains(t, err, `expected "string", got "integer"`, name)
		}

		// A path under neither directory names both.
		outside := filepath.Join(base, "other", "s.json")

		_, err := reg.Load(t.Context(), schema.File(outside))
		requireLoadError(t, err, schema.ErrFileNotAllowed)
		require.ErrorContains(t, err,
			"read "+outside+": file not allowed: not under "+tenant+" or "+shared+
				", the directories of the registry's roots",
		)
	})

	t.Run("a refusal names every directory", func(t *testing.T) {
		t.Parallel()

		a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
		outside := yamltest.WriteFile(t, t.TempDir(), "s.json", nameSchema)

		tcs := map[string]struct {
			dirs []string
			want string
		}{
			"one root": {
				dirs: []string{a},
				want: "not under " + a + ", the directory of the registry's root",
			},
			"two roots": {
				dirs: []string{a, b},
				want: "not under " + a + " or " + b + ", the directories of the registry's roots",
			},
			"three roots": {
				dirs: []string{a, b, c},
				want: "not under " + a + ", " + b + ", or " + c + ", the directories of the registry's roots",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				opts := make([]schema.AccessOption, 0, len(tc.dirs))
				for _, dir := range tc.dirs {
					opts = append(opts, allowRoot(t, dir))
				}

				reg := schema.NewRegistry(withAccess(t, opts...))

				_, err := reg.Load(t.Context(), schema.File(outside))
				requireLoadError(t, err, schema.ErrFileNotAllowed)
				require.ErrorContains(t, err, tc.want)
			})
		}
	})

	t.Run("the first root that holds a path decides its read", func(t *testing.T) {
		t.Parallel()

		// The link in inner leads out of inner but stays in outer.
		outer := t.TempDir()
		inner := filepath.Join(outer, "inner")
		yamltest.WriteFile(t, outer, "s.json", nameSchema)
		yamltest.WriteFile(t, inner, "keep.json", nameSchema)
		yamltest.SymlinkOrSkip(t, filepath.Join("..", "s.json"), filepath.Join(inner, "link.json"))

		link := filepath.Join(inner, "link.json")

		// The same link, named through a link to the parent of outer and
		// through a directory link in inner that leads out of it.
		via := filepath.Join(t.TempDir(), "via")
		yamltest.SymlinkOrSkip(t, filepath.Dir(outer), via)
		yamltest.SymlinkOrSkip(t, "..", filepath.Join(inner, "up"))

		viaOuter := filepath.Join(via, filepath.Base(outer))
		viaInner := filepath.Join(viaOuter, "inner")
		viaLink := filepath.Join(viaInner, "link.json")
		viaUp := filepath.Join(viaInner, "up", "s.json")

		// The roots can name their directories through the link too. The
		// order of the roots decides, whichever way the roots and the path
		// name the directories.
		tcs := map[string]struct {
			path string
			dirs []string
			err  error
		}{
			"inner first": {path: link, dirs: []string{inner, outer}, err: schema.ErrLoad},
			"outer first": {path: link, dirs: []string{outer, inner}},
			"inner first, outer named through a link": {
				path: link,
				dirs: []string{inner, viaOuter},
				err:  schema.ErrLoad,
			},
			"inner first, outer and path named through a link": {
				path: viaLink,
				dirs: []string{inner, viaOuter},
				err:  schema.ErrLoad,
			},
			"inner first, inner named through a link": {
				path: link,
				dirs: []string{viaInner, outer},
				err:  schema.ErrLoad,
			},
			"inner first, inner and path named through a link": {
				path: viaLink,
				dirs: []string{viaInner, outer},
				err:  schema.ErrLoad,
			},
			"outer first, outer named through a link": {path: link, dirs: []string{viaOuter, inner}},
			"outer first, outer and path named through a link": {
				path: viaLink,
				dirs: []string{viaOuter, inner},
			},
			"outer first, inner named through a link": {path: link, dirs: []string{outer, viaInner}},
			"outer first, inner and path named through a link": {
				path: viaLink,
				dirs: []string{outer, viaInner},
			},
			"inner first, through a link": {
				path: viaLink,
				dirs: []string{inner, outer},
				err:  schema.ErrLoad,
			},
			"outer first, through a link": {path: viaLink, dirs: []string{outer, inner}},
			"inner first, directory link through a link": {
				path: viaUp,
				dirs: []string{inner, outer},
				err:  schema.ErrLoad,
			},
			"outer first, directory link through a link": {path: viaUp, dirs: []string{outer, inner}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				opts := make([]schema.AccessOption, 0, len(tc.dirs))
				for _, dir := range tc.dirs {
					opts = append(opts, allowRoot(t, dir))
				}

				reg := schema.NewRegistry(withAccess(t, opts...))

				data, err := reg.Load(t.Context(), schema.File(tc.path))
				if tc.err != nil {
					// The root of inner refuses the link, and the root of
					// outer does not get the path.
					require.ErrorIs(t, err, tc.err)
					require.NotErrorIs(t, err, schema.ErrFileNotAllowed)

					return
				}

				require.NoError(t, err)
				assert.JSONEq(t, nameSchema, string(data))
			})
		}
	})

	t.Run("a link outside every root resolves one name at a time", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		root := filepath.Join(base, "root")
		out := filepath.Join(base, "out")

		yamltest.WriteFile(t, root, "s.json", nameSchema)
		yamltest.WriteFile(t, out, "keep.json", nameSchema)

		// Relative links whose targets step up with ".." and stay with ".",
		// and two links that lead to each other.
		yamltest.SymlinkOrSkip(t, "..", filepath.Join(out, "up"))
		yamltest.SymlinkOrSkip(t, "./../.", filepath.Join(out, "up2"))
		yamltest.SymlinkOrSkip(t, "b", filepath.Join(out, "a"))
		yamltest.SymlinkOrSkip(t, "a", filepath.Join(out, "b"))

		tcs := map[string]struct {
			path string
			err  error
		}{
			"parent link": {path: filepath.Join(out, "up", "root", "s.json")},
			"parent link with current names": {
				path: filepath.Join(out, "up2", "root", "s.json"),
			},
			"link loop": {
				path: filepath.Join(out, "a", "root", "s.json"),
				err:  schema.ErrFileNotAllowed,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(withAccess(t, allowRoot(t, root)))

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

	t.Run("a parent name after a link steps up from its target", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		a := filepath.Join(base, "a")
		b := filepath.Join(base, "b")
		out := filepath.Join(base, "out")

		yamltest.WriteFiles(t, base, map[string]string{
			"a/d1/t/s.json": `{"title": "a/d1/t"}`,
			"a/t/s.json":    `{"title": "a/t"}`,
			"a/x/y/keep":    ``,
			"a/b/s.json":    `{"title": "a/b"}`,
			"b/s.json":      `{"title": "b"}`,
			"out/keep":      ``,
		})
		require.NoError(t, os.MkdirAll(filepath.Join(a, "d1", "d2"), 0o750))

		// Links inside a: a relative link, which the root follows, and an
		// absolute one, which it refuses.
		yamltest.SymlinkOrSkip(t, filepath.Join("d1", "d2"), filepath.Join(a, "sub"))
		yamltest.SymlinkOrSkip(t, filepath.Join(a, "d1", "d2"), filepath.Join(a, "abs"))
		yamltest.SymlinkOrSkip(t, filepath.Join("x", "y"), filepath.Join(a, "xy"))

		// Links outside every root whose targets step up after a name in a.
		// The targets join their names by hand, because filepath.Join
		// removes each "..".
		link := func(name string, target ...string) string {
			yamltest.SymlinkOrSkip(t, strings.Join(target, string(filepath.Separator)), filepath.Join(out, name))

			return filepath.Join(out, name)
		}

		sub := link("sub", a, "sub", "..")
		abs := link("abs", a, "abs", "..")
		xy := link("xy", a, "xy", "..", "..")
		d1 := link("d1", a, "d1", "d2", "..")
		up := link("up", a, "..")

		tcs := map[string]struct {
			path string
			dirs []string
			err  error
		}{
			"link in the root": {path: filepath.Join(sub, "t", "s.json"), dirs: []string{a}},
			"absolute link in a root": {
				path: filepath.Join(abs, "t", "s.json"),
				dirs: []string{a},
				err:  schema.ErrLoad,
			},
			"link in the first root":  {path: filepath.Join(xy, "b", "s.json"), dirs: []string{a, b}},
			"link in the second root": {path: filepath.Join(xy, "b", "s.json"), dirs: []string{b, a}},
			"directory in the root":   {path: filepath.Join(d1, "t", "s.json"), dirs: []string{a}},
			"out of the first root":   {path: filepath.Join(up, "b", "s.json"), dirs: []string{a, b}},
			"out of the second root":  {path: filepath.Join(up, "b", "s.json"), dirs: []string{b, a}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				opts := make([]schema.AccessOption, 0, len(tc.dirs))
				for _, dir := range tc.dirs {
					opts = append(opts, allowRoot(t, dir))
				}

				reg := schema.NewRegistry(withAccess(t, opts...))

				data, err := reg.Load(t.Context(), schema.File(tc.path))
				if tc.err != nil {
					// The root of a refuses its absolute link.
					require.ErrorIs(t, err, tc.err)
					require.NotErrorIs(t, err, schema.ErrFileNotAllowed)

					return
				}

				require.NoError(t, err)

				// The registry reads the file that the OS names.
				want, err := os.ReadFile(tc.path)
				require.NoError(t, err)
				assert.JSONEq(t, string(want), string(data))
			})
		}
	})

	t.Run("a root named through a link in another root", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		a := filepath.Join(base, "a")
		out := filepath.Join(base, "out")

		yamltest.WriteFiles(t, base, map[string]string{
			"a/real/s.json":   nameSchema,
			"a/real/b/c/keep": ``,
			"out/keep":        ``,
		})

		// The inner root opens a/real/b through the link lnkb in a. The link
		// up in a/real/b leads out of the inner root but stays in a.
		inner := filepath.Join(a, "lnkb")
		yamltest.SymlinkOrSkip(t, filepath.Join("real", "b"), inner)
		yamltest.SymlinkOrSkip(t, "..", filepath.Join(a, "real", "b", "up"))

		// Links outside every root that lead to inner, one of them with a
		// ".." after a name in it. The target joins its names by hand,
		// because filepath.Join removes each "..".
		yamltest.SymlinkOrSkip(t, inner, filepath.Join(out, "l"))
		yamltest.SymlinkOrSkip(t,
			strings.Join([]string{inner, "c", ".."}, string(filepath.Separator)),
			filepath.Join(out, "p"),
		)

		// Each spelling names a/real/s.json through up.
		paths := map[string]string{
			"inner as written":          filepath.Join(inner, "up", "s.json"),
			"inner with links resolved": filepath.Join(a, "real", "b", "up", "s.json"),
			"link outside every root":   filepath.Join(out, "l", "up", "s.json"),
			"link with a parent name":   filepath.Join(out, "p", "up", "s.json"),
		}

		// The root of inner refuses up, and the root of a follows it,
		// whichever way the path names inner.
		tcs := map[string]struct {
			dirs []string
			err  error
		}{
			"inner first": {dirs: []string{inner, a}, err: schema.ErrLoad},
			"outer first": {dirs: []string{a, inner}},
		}

		for name, tc := range tcs {
			for spelling, path := range paths {
				t.Run(name+", "+spelling, func(t *testing.T) {
					t.Parallel()

					opts := make([]schema.AccessOption, 0, len(tc.dirs))
					for _, dir := range tc.dirs {
						opts = append(opts, allowRoot(t, dir))
					}

					reg := schema.NewRegistry(withAccess(t, opts...))

					data, err := reg.Load(t.Context(), schema.File(path))
					if tc.err != nil {
						require.ErrorIs(t, err, tc.err)
						require.NotErrorIs(t, err, schema.ErrFileNotAllowed)

						return
					}

					require.NoError(t, err)
					assert.JSONEq(t, nameSchema, string(data))
				})
			}
		}
	})

	t.Run("a link into another root stays refused", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		tenant := filepath.Join(base, "tenant")
		shared := filepath.Join(base, "shared")
		target := yamltest.WriteFile(t, shared, "s.json", nameSchema)
		yamltest.WriteFile(t, tenant, "keep.json", nameSchema)
		yamltest.SymlinkOrSkip(t, target, filepath.Join(tenant, "link.json"))
		yamltest.SymlinkOrSkip(t, shared, filepath.Join(tenant, "dirlink"))

		// A link to base, so a path can reach tenant through it, and a link
		// outside every root that leads to the directory link.
		outside := t.TempDir()
		via := filepath.Join(outside, "via")
		yamltest.SymlinkOrSkip(t, base, via)
		yamltest.SymlinkOrSkip(t, filepath.Join(tenant, "dirlink"), filepath.Join(outside, "todirlink"))

		tcs := map[string]struct {
			dirs []string
			path string
		}{
			"file link through a link, tenant first": {
				dirs: []string{tenant, shared},
				path: filepath.Join(via, "tenant", "link.json"),
			},
			"file link through a link, shared first": {
				dirs: []string{shared, tenant},
				path: filepath.Join(via, "tenant", "link.json"),
			},
			"directory link through a link, tenant first": {
				dirs: []string{tenant, shared},
				path: filepath.Join(via, "tenant", "dirlink", "s.json"),
			},
			"directory link through a link, shared first": {
				dirs: []string{shared, tenant},
				path: filepath.Join(via, "tenant", "dirlink", "s.json"),
			},
			"link to a directory link, tenant first": {
				dirs: []string{tenant, shared},
				path: filepath.Join(outside, "todirlink", "s.json"),
			},
			"link to a directory link, shared first": {
				dirs: []string{shared, tenant},
				path: filepath.Join(outside, "todirlink", "s.json"),
			},
			"file link, tenant first": {
				dirs: []string{tenant, shared},
				path: filepath.Join(tenant, "link.json"),
			},
			"file link, shared first": {
				dirs: []string{shared, tenant},
				path: filepath.Join(tenant, "link.json"),
			},
			"directory link, tenant first": {
				dirs: []string{tenant, shared},
				path: filepath.Join(tenant, "dirlink", "s.json"),
			},
			"directory link, shared first": {
				dirs: []string{shared, tenant},
				path: filepath.Join(tenant, "dirlink", "s.json"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				opts := make([]schema.AccessOption, 0, len(tc.dirs))
				for _, dir := range tc.dirs {
					opts = append(opts, allowRoot(t, dir))
				}

				reg := schema.NewRegistry(withAccess(t, opts...))

				// The root of tenant holds the path as written and refuses
				// the link, whatever the order of the roots.
				_, err := reg.Load(t.Context(), schema.File(tc.path))
				require.ErrorIs(t, err, schema.ErrLoad)
				require.NotErrorIs(t, err, schema.ErrFileNotAllowed)

				data, err := reg.Load(t.Context(), schema.File(target))
				require.NoError(t, err)
				assert.JSONEq(t, nameSchema, string(data))
			})
		}
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
				err:  schema.ErrFileNotAllowed,
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
					// The os.Root refuses the link with an error of its own.
					require.NotErrorIs(t, err, schema.ErrFileNotAllowed)

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
			require.ErrorIs(t, err, schema.ErrFileNotAllowed, dir)
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

// A root opened as "../schemas" in a working directory that the shell
// names through a link reads the directory that the OS opened, and
// FileOrURL names the schema there by the same path. The test changes
// the process's working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestAllowRoot_DotDotOutOfSymlinkedWorkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, map[string]string{
		"real/sub/.keep":      "",
		"real/schemas/s.json": `{"type": "object"}`,
	})

	cwd := filepath.Join(dir, "cwdlink")
	yamltest.SymlinkOrSkip(t, filepath.Join("real", "sub"), cwd)

	t.Chdir(cwd)

	access, err := schema.NewAccess(allowRoot(t, "../schemas"))
	require.NoError(t, err)

	ref, err := schema.FileOrURL(cwd, "../schemas/s.json")
	require.NoError(t, err)

	reg := schema.NewRegistry(schema.WithAccess(access))

	data, err := reg.Load(t.Context(), ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type": "object"}`, string(data))
}
