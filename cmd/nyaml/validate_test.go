package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
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

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

// nameSchema is the schema the validateFile tests apply to every document
// of their fixtures. It requires a "name" key.
var nameSchema = []byte(`{
	"type": "object",
	"properties": {"name": {"type": "string"}},
	"required": ["name"]
}`)

// oldSchema is [nameSchema] with a deprecated "old" key, so a document
// that sets "old" passes with a warning.
const oldSchema = `{
	"type": "object",
	"properties": {"name": {"type": "string"}, "old": {"deprecated": true}},
	"required": ["name"]
}`

// nameRegistry returns a registry that applies [nameSchema] to every
// document.
func nameRegistry() *schema.Registry {
	return schema.NewRegistry(schema.WithResolvers(schema.Embedded(nameSchema)))
}

// relToWorkDir returns path relative to the working directory.
func relToWorkDir(t *testing.T, path string) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	rel, err := filepath.Rel(wd, path)
	require.NoError(t, err)

	return rel
}

// writeOrSkip writes content to the file name in dir. It skips the test
// when the write fails, since some file systems, such as those on Windows,
// reject control characters in a name.
func writeOrSkip(t *testing.T, dir, name, content string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
	if err != nil {
		t.Skipf("write %q: %v", name, err)
	}
}

// quietValidateCmd returns nyaml validate set to run with args, to write
// its stdout to out and its stderr to errOut, and to print no error
// itself.
func quietValidateCmd(out, errOut io.Writer, args ...string) *cobra.Command {
	cmd := validateCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)

	return cmd
}

// linkedDir returns a temporary directory that holds the directory
// sub/deep and a symlink named link that leads to it. It skips the test
// where the OS makes no symlink, and on Windows, which reads a ".." after
// a symlink differently.
func linkedDir(t *testing.T, link string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	makeDir(t, dir, "sub", "deep")

	yamltest.SymlinkOrSkip(t, filepath.Join("sub", "deep"), filepath.Join(dir, link))

	return dir
}

func TestValidateFile(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		// Positions of every document the joined error reports, in file
		// order, each as the "line:col:" that follows the file path. Valid
		// documents have no entry, and a valid file has none.
		want []string
	}{
		"every document valid": {content: stringtest.LinesLF("name: a", "---", "name: b", "---", "name: c")},
		"first and last document invalid": {
			content: stringtest.LinesLF("value: 1", "---", "name: b", "---", "value: 3"),
			want:    []string{"1:1:", "5:1:"},
		},
		"first two documents invalid": {
			content: stringtest.LinesLF("value: 1", "---", "value: 2", "---", "name: c"),
			want:    []string{"1:1:", "3:1:"},
		},
		"middle document invalid": {
			content: stringtest.LinesLF("name: a", "---", "value: 2", "---", "name: c"),
			want:    []string{"3:1:"},
		},
		"comment above the first header": {
			content: yamltest.SchemaDirective("./s.json", stringtest.LinesLF("---", "name: a")),
		},
		"comment above an invalid document": {
			content: stringtest.LinesLF("# a preamble", "---", "value: 1"),
			want:    []string{"3:1:"},
		},
		"invalid document after consecutive headers": {
			content: stringtest.LinesLF("name: a", "---", "---", "value: 1"),
			want:    []string{"4:1:"},
		},
		"only document does not parse": {
			content: "name: [\n",
			want:    []string{"1:7:"},
		},
		"byte that is not UTF-8": {
			content: stringtest.LinesLF("name: a", "---", "name: caf\xe9"),
			want:    []string{"3:10:"},
		},
		"syntax errors beside violations": {
			content: stringtest.LinesLF(
				"name: a",
				"---",
				"name: [",
				"---",
				"value: 3",
				"---",
				"name: @x",
				"---",
				"value: 5",
			),
			want: []string{"3:7:", "5:1:", "7:7:", "9:1:"},
		},
		"valid documents around one that does not parse": {
			content: stringtest.LinesLF("name: a", "---", "name: [", "---", "name: c"),
			want:    []string{"3:7:"},
		},
		// The header parses together with the document above it, so both
		// documents carry the one error, and the file reports it once.
		"header after an anchor with no value": {
			content: stringtest.LinesLF("name: &x", "---", "name: [", "---", "value: 5"),
			want:    []string{"3:7:", "5:1:"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := yamltest.WriteFile(t, t.TempDir(), "config.yaml", tc.content)

			err := validateFile(t.Context(), path, nameRegistry())
			if len(tc.want) == 0 {
				require.NoError(t, err)

				return
			}

			var got []string

			for b := range niceyaml.Bindings(err) {
				msg, ok := strings.CutPrefix(b.Error(), path+":")
				require.True(t, ok, b.Error())

				pos, _, _ := strings.Cut(msg, " ")
				got = append(got, pos)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateFileEmpty(t *testing.T) {
	t.Parallel()

	// A file of text that is only a "..." marker holds one empty document,
	// so it validates as an empty file does.
	tcs := map[string]struct {
		reg     *schema.Registry
		content string
		// Message after the file path, or empty when the file is valid.
		want string
	}{
		"empty file against a schema": {
			reg:     nameRegistry(),
			content: "",
			want:    `: $: expected "object", got "null"`,
		},
		"document end marker against a schema": {
			reg:     nameRegistry(),
			content: "...\n",
			want:    `: $: expected "object", got "null"`,
		},
		"document end marker with no schema": {
			reg: schema.NewRegistry(
				schema.WithAccess(schema.Unrestricted),
				schema.WithResolvers(schema.Directive()),
				schema.WithRequireSchema(false),
			),
			content: "...\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := yamltest.WriteFile(t, t.TempDir(), "config.yaml", tc.content)

			err := validateFile(t.Context(), path, tc.reg)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Equal(t, path+tc.want, err.Error())
		})
	}
}

func TestValidateFileRoutesOnAbsolutePath(t *testing.T) {
	t.Parallel()

	// SchemaStore routes on patterns that name parent directories, so the
	// resolver must see the absolute path even when the user types a
	// relative one. Messages still name the file as the user typed it.
	path := yamltest.WriteFile(t, t.TempDir(), "config.yaml", "value: 1\n")

	rel := relToWorkDir(t, path)
	require.False(t, filepath.IsAbs(rel))

	var got string

	reg := schema.NewRegistry(schema.WithResolvers(
		schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			got = doc.FilePath()

			return schema.Embedded(nameSchema), nil
		}),
	))

	err := validateFile(t.Context(), rel, reg)
	require.Error(t, err)
	assert.Equal(t, path, got)
	assert.Contains(t, err.Error(), rel+":1:1: ")
}

func TestValidateFileDotDotAfterSymlink(t *testing.T) {
	t.Parallel()

	// With ldir linking to sub/deep, the OS reads ldir/../x.yaml as
	// sub/x.yaml, so the directive's ./s.json names sub/s.json.
	dir := linkedDir(t, "ldir")
	sub := filepath.Join(dir, "sub")

	yamltest.WriteFile(t, sub, "s.json", string(nameSchema))
	yamltest.WriteFile(t, sub, "x.yaml", yamltest.SchemaDirective("./s.json", "name: a\n"))

	// Joining the name with filepath.Join would clean it to x.yaml.
	path := dir + "/ldir/../x.yaml"

	reg := schema.NewRegistry(schema.WithAccess(schema.Unrestricted), schema.WithResolvers(schema.Directive()))

	require.NoError(t, validateFile(t.Context(), path, reg))
}

func TestBuildRegistryRefs(t *testing.T) {
	t.Parallel()

	// The schema names a file that does not exist from a property a
	// document can leave out.
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "s.json")

	yamltest.WriteFiles(t, dir, map[string]string{
		"s.json":       `{"properties": {"name": {"type": "string"}, "extra": {"$ref": "missing.json"}}}`,
		"skips.yaml":   yamltest.SchemaDirective("./s.json", "name: a\n"),
		"reaches.yaml": yamltest.SchemaDirective("./s.json", "extra: 1\n"),
	})

	t.Run("--schema fails before any file", func(t *testing.T) {
		t.Parallel()

		_, err := buildRegistry(t.Context(), schemaPath, schema.Unrestricted, nil)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrNotExist)
		assert.True(t, strings.HasPrefix(err.Error(), "--schema: "), err.Error())
		assert.Contains(t, err.Error(), filepath.Join(dir, "missing.json"))
	})

	t.Run("a directive fails only the document that reaches the ref", func(t *testing.T) {
		t.Parallel()

		reg, err := buildRegistry(t.Context(), "", schema.Unrestricted, nil)
		require.NoError(t, err)

		require.NoError(t, validateFile(t.Context(), filepath.Join(dir, "skips.yaml"), reg))

		err = validateFile(t.Context(), filepath.Join(dir, "reaches.yaml"), reg)
		require.ErrorIs(t, err, schema.ErrValidate)
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestBuildRegistrySchemaDotDotAfterSymlink(t *testing.T) {
	t.Parallel()

	// With ldir linking to sub/deep, the OS reads ldir/../schema.json as
	// sub/schema.json, which accepts a string. The schema.json beside
	// ldir, which cleaning the path as text would name, rejects one.
	dir := linkedDir(t, "ldir")

	yamltest.WriteFile(t, dir, "sub/schema.json", `{"type": "string"}`)
	yamltest.WriteFile(t, dir, "schema.json", `{"type": "integer"}`)

	yamlPath := yamltest.WriteFile(t, dir, "x.yaml", "hello\n")
	rel := relToWorkDir(t, dir)

	// The test builds each ref as text, since filepath.Join would clean it.
	tcs := map[string]struct {
		ref string
	}{
		"absolute": {ref: dir + "/ldir/../schema.json"},
		"relative": {ref: rel + "/ldir/../schema.json"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := buildRegistry(t.Context(), tc.ref, schema.Unrestricted, nil)
			require.NoError(t, err)

			require.NoError(t, validateFile(t.Context(), yamlPath, reg))
		})
	}
}

// A relative ref needs a colon in its first element to test this, so the
// test changes the working directory and does not run in parallel.
//
//nolint:paralleltest // See above.
func TestBuildRegistrySchemaColonDotDotAfterSymlink(t *testing.T) {
	// Every link leads to sub/deep, so the OS reads each ref as
	// sub/schema.json, which accepts a string. Cleaning a ref as text
	// would name a schema.json beside the link, which rejects one or
	// does not exist.
	dir := linkedDir(t, "v1:ldir")

	makeDir(t, dir, "cfg:v2")
	makeDir(t, dir, "file:")
	yamltest.WriteFile(t, dir, "sub/schema.json", `{"type": "string"}`)
	yamltest.WriteFile(t, dir, "schema.json", `{"type": "integer"}`)

	yamlPath := yamltest.WriteFile(t, dir, "x.yaml", "hello\n")

	require.NoError(t, os.Symlink(filepath.Join("..", "sub", "deep"), filepath.Join(dir, "cfg:v2", "ldir")))
	require.NoError(t, os.Symlink(filepath.Join("..", "sub", "deep"), filepath.Join(dir, "file:", "ldir")))

	t.Chdir(dir)

	tcs := map[string]struct {
		ref string
	}{
		"colon in the symlink name":               {ref: "v1:ldir/../schema.json"},
		"colon in a directory before the symlink": {ref: "cfg:v2/ldir/../schema.json"},
		// FileOrURL reads a file URL whose host is not localhost as a
		// relative path, here file:/ldir/../schema.json.
		"file URL with a host": {ref: "file://ldir/../schema.json"},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			reg, err := buildRegistry(t.Context(), tc.ref, schema.Unrestricted, nil)
			require.NoError(t, err)

			require.NoError(t, validateFile(t.Context(), yamlPath, reg))
		})
	}
}

func TestValidateFileUnreadable(t *testing.T) {
	t.Parallel()

	// The read error already names the file, so the message names it once.
	path := filepath.Join(t.TempDir(), "missing.yaml")

	err := validateFile(t.Context(), path, schema.NewRegistry())
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Equal(t, 1, strings.Count(err.Error(), path))
}

func TestValidateCmdSchemaError(t *testing.T) {
	t.Parallel()

	// A --schema that cannot load or compile fails the command once,
	// before it reads any file, however many documents the files hold.
	tcs := map[string]struct {
		err error
		// Contents of the schema file. With neither this nor url set, the
		// reference names a file that does not exist.
		content string
		// Serves the schema from a server that answers every request
		// with a 500.
		url bool
	}{
		"missing file": {err: schema.ErrLoad},
		"invalid JSON": {content: `{"type": 12`, err: schema.ErrCompile},
		"invalid YAML": {content: "type: [object\n", err: schema.ErrCompile},
		// One of the three documents sets no "a", so its validation would
		// never reach the $ref.
		"$ref names a missing file": {
			content: `{"properties": {"a": {"$ref": "defs.json"}}}`,
			err:     schema.ErrLoad,
		},
		"URL fails": {url: true, err: schema.ErrLoad},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			var fetches atomic.Int32

			ref := filepath.Join(dir, "schema.json")

			switch {
			case tc.url:
				// A test in another process can dial a port it released,
				// and this server may hold that port by then, so the
				// handler counts only requests for the schema path.
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/schema.json" {
						fetches.Add(1)
					}

					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(srv.Close)

				ref = srv.URL + "/schema.json"

			case tc.content != "":
				yamltest.WriteFile(t, dir, "schema.json", tc.content)
			}

			// Three documents across two files, so a schema loaded per
			// document would report three times.
			yamlPaths := []string{
				yamltest.WriteFile(t, dir, "one.yaml", "a: 1\n"),
				yamltest.WriteFile(t, dir, "two.yaml", stringtest.LinesLF("a: 1", "---", "b: 2")),
			}

			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

			err := quietValidateCmd(out, errOut, append([]string{"--schema", ref}, yamlPaths...)...).Execute()
			require.ErrorIs(t, err, tc.err)

			msg := err.Error()
			assert.True(t, strings.HasPrefix(msg, "--schema: "), msg)
			assert.Equal(t, 1, strings.Count(msg, tc.err.Error()), msg)

			for _, yamlPath := range yamlPaths {
				assert.NotContains(t, msg, yamlPath)
			}

			assert.Empty(t, out.String())
			assert.Empty(t, errOut.String())

			if tc.url {
				assert.Equal(t, int32(1), fetches.Load())
			}
		})
	}
}

func TestValidateCmdHelp(t *testing.T) {
	t.Parallel()

	// The help names every source of a document's schema, so a reader
	// learns that a run without --schema can still download one. It also
	// names each exit status, so a script tells an invalid document from a
	// run that could not check one, and names what each stream carries.
	// It names each flag that confines what a run reads and fetches.
	long := validateCmd().Long
	for _, want := range []string{
		"--schema", "$schema=", "$schema=none", "SchemaStore", "$ref", "--strict",
		"--schemastore=false", "--offline", "--schema-root",
		"Stdout", "Stderr", "Exits 1", "Exits 2",
	} {
		assert.Contains(t, long, want)
	}
}

// schemaServer serves the schemas of the schema policy tests and records
// the path of each request it answers.
type schemaServer struct {
	*httptest.Server

	mu    sync.Mutex
	paths []string
}

// newSchemaServer starts a [schemaServer]. It serves a SchemaStore catalog
// at /catalog.json whose entry for plain.yaml names /value.json, a schema
// that requires a "value" key. It serves [nameSchema] at /s.json.
func newSchemaServer(t *testing.T) *schemaServer {
	t.Helper()

	s := &schemaServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()

		s.paths = append(s.paths, r.URL.Path)

		s.mu.Unlock()

		var body string

		switch r.URL.Path {
		case "/catalog.json":
			body = `{"schemas": [{"name": "plain", "fileMatch": ["plain.yaml"], "url": "` +
				s.URL + `/value.json"}]}`

		case "/value.json":
			body = `{"required": ["value"]}`

		case "/s.json":
			body = string(nameSchema)

		default:
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, err := io.WriteString(w, body)
		assert.NoError(t, err)
	}))
	t.Cleanup(s.Close)

	return s
}

// requested returns the paths that the server answered, each once, in
// sorted order.
func (s *schemaServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	paths := slices.Clone(s.paths)
	slices.Sort(paths)

	return slices.Compact(paths)
}

func TestValidateCmdSchemaPolicy(t *testing.T) {
	t.Parallel()

	// Each file holds a document that passes against the schema its
	// directive names. The catalog names a schema for plain.yaml that
	// it fails. The test directory holds root, which holds every file to
	// validate and s.json, and other, which holds a second s.json.
	tcs := map[string]struct {
		err error
		// Flags before the files. The value "ROOT" stands for the path of
		// the root directory.
		args []string
		// The result lines for local.yaml, plain.yaml, remote.yaml, and
		// outside.yaml, in that order.
		want []string
		// The paths that the run requests from the server, sorted.
		requested []string
		// The status the run exits with.
		exit int
	}{
		"defaults": {
			want:      []string{"valid", "invalid", "valid", "valid"},
			requested: []string{"/catalog.json", "/s.json", "/value.json"},
			exit:      exitInvalid,
		},
		"without SchemaStore": {
			args:      []string{"--schemastore=false"},
			want:      []string{"valid", "valid", "valid", "valid"},
			requested: []string{"/s.json"},
		},
		"offline": {
			args: []string{"--offline"},
			want: []string{"valid", "valid", "failed", "valid"},
			err:  schema.ErrURLNotAllowed,
			exit: exitFailure,
		},
		"offline with SchemaStore": {
			args: []string{"--offline", "--schemastore=true"},
			want: []string{"valid", "valid", "failed", "valid"},
			err:  schema.ErrURLNotAllowed,
			exit: exitFailure,
		},
		"schema root": {
			args:      []string{"--schema-root", "ROOT"},
			want:      []string{"valid", "invalid", "valid", "failed"},
			err:       schema.ErrFileNotAllowed,
			requested: []string{"/catalog.json", "/s.json", "/value.json"},
			exit:      exitFailure,
		},
		"offline schema root": {
			args: []string{"--offline", "--schema-root", "ROOT"},
			want: []string{"valid", "valid", "failed", "failed"},
			err:  schema.ErrFileNotAllowed,
			exit: exitFailure,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := newSchemaServer(t)

			dir := t.TempDir()
			root := filepath.Join(dir, "root")

			yamltest.WriteFiles(t, dir, map[string]string{
				"other/s.json":      string(nameSchema),
				"root/s.json":       string(nameSchema),
				"root/local.yaml":   yamltest.SchemaDirective("./s.json", "name: a\n"),
				"root/plain.yaml":   "name: a\n",
				"root/remote.yaml":  yamltest.SchemaDirective(srv.URL+"/s.json", "name: a\n"),
				"root/outside.yaml": yamltest.SchemaDirective("../other/s.json", "name: a\n"),
			})

			var args []string

			for _, arg := range tc.args {
				arg = strings.ReplaceAll(arg, "ROOT", root)
				args = append(args, arg)
			}

			files := []string{"local.yaml", "plain.yaml", "remote.yaml", "outside.yaml"}

			var want strings.Builder

			for i, file := range files {
				args = append(args, filepath.Join(root, file))
				want.WriteString(filepath.Join(root, file) + ": " + tc.want[i] + "\n")
			}

			out := &bytes.Buffer{}

			cmd := validateCmd(schemastore.WithCatalog(schema.URL(srv.URL + "/catalog.json")))
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)

			err := cmd.Execute()
			assert.Equal(t, tc.exit, exitCode(err), "%v", err)
			assert.Equal(t, want.String(), out.String())
			assert.Equal(t, tc.requested, srv.requested())

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestValidateCmdSchemaPolicyFlag(t *testing.T) {
	t.Parallel()

	// A --schema that the policy flags do not allow fails the command
	// once, before it reads any file or requests anything.
	tcs := map[string]struct {
		err error
		// Flags before the file. The value "ROOT" stands for the path of
		// the root directory, and "URL" for the URL of the server.
		args []string
		// The prefix of the error message.
		prefix string
	}{
		"offline URL": {
			args:   []string{"--offline", "--schema", "URL/s.json"},
			err:    schema.ErrURLNotAllowed,
			prefix: "--schema: ",
		},
		"file outside the root": {
			args:   []string{"--schema-root", "ROOT", "--schema", "ROOT/../other/s.json"},
			err:    schema.ErrFileNotAllowed,
			prefix: "--schema: ",
		},
		"missing root": {
			args:   []string{"--schema-root", "ROOT/missing"},
			err:    fs.ErrNotExist,
			prefix: "--schema-root: ",
		},
		// An empty value, such as from an unset shell variable, fails
		// rather than lift the confinement.
		"empty root": {
			args:   []string{"--schema-root", "", "--schema", "ROOT/../other/s.json"},
			err:    errEmptyPath,
			prefix: "--schema-root: empty path",
		},
		"empty root with equals": {
			args:   []string{"--schema-root=", "--schema", "ROOT/../other/s.json"},
			err:    errEmptyPath,
			prefix: "--schema-root: empty path",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := newSchemaServer(t)

			dir := t.TempDir()
			root := filepath.Join(dir, "root")

			yamltest.WriteFiles(t, dir, map[string]string{
				"other/s.json":    string(nameSchema),
				"root/s.json":     string(nameSchema),
				"root/local.yaml": "name: a\n",
			})

			var args []string

			for _, arg := range tc.args {
				arg = strings.ReplaceAll(arg, "ROOT", root)
				arg = strings.ReplaceAll(arg, "URL", srv.URL)
				args = append(args, arg)
			}

			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

			err := quietValidateCmd(out, errOut, append(args, filepath.Join(root, "local.yaml"))...).Execute()
			require.ErrorIs(t, err, tc.err)
			assert.True(t, strings.HasPrefix(err.Error(), tc.prefix), err.Error())
			assert.Equal(t, exitFailure, exitCode(err))
			assert.Empty(t, out.String())
			assert.Empty(t, errOut.String())
			assert.Empty(t, srv.requested())
		})
	}

	t.Run("schema inside the root", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		schemaPath := yamltest.WriteFile(t, dir, "s.json", string(nameSchema))
		path := yamltest.WriteFile(t, dir, "local.yaml", "name: a\n")

		out := &bytes.Buffer{}

		args := []string{"--offline", "--schema-root", dir, "--schema", schemaPath, path}

		err := quietValidateCmd(out, io.Discard, args...).Execute()
		require.NoError(t, err)
		assert.Equal(t, path+": valid\n", out.String())
	})
}

func TestValidateCmdCanceled(t *testing.T) {
	t.Parallel()

	// A canceled run stops and reports the cancellation once, however
	// many files and documents are left.
	tcs := map[string]struct {
		// When set, the run is canceled while the first document fetches
		// its schema. Otherwise it is canceled before it starts.
		midRun bool
	}{
		"before the first file": {},
		"during a schema fetch": {midRun: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())

			doc := "name: a\n"
			if tc.midRun {
				srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					cancel()
					<-r.Context().Done()
				}))
				t.Cleanup(srv.Close)

				doc = yamltest.SchemaDirective(srv.URL+"/s.json", doc)
			} else {
				cancel()
			}

			body := strings.Join([]string{doc, doc, doc}, "---\n")
			dir := t.TempDir()

			var args []string

			for _, name := range []string{"a.yaml", "b.yaml", "c.yaml"} {
				args = append(args, yamltest.WriteFile(t, dir, name, body))
			}

			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

			err := quietValidateCmd(out, errOut, args...).ExecuteContext(ctx)
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, 1, strings.Count(err.Error(), context.Canceled.Error()), err.Error())
			assert.Empty(t, out.String())
			assert.Empty(t, errOut.String())
		})
	}
}

func TestValidateCmdOutput(t *testing.T) {
	t.Parallel()

	// The result lines go to the stdout writer the caller set on the
	// command, so embedding the command and redirecting its output
	// captures them. Stdout carries one result line for each file, in the
	// order of the arguments, and nothing else.
	tcs := map[string]struct {
		// Contents of the files to write to the test directory, by name.
		files map[string]string
		// Arguments after the schema flag. Each argument that does not
		// start with "-" is relative to the test directory.
		args []string
		// The result lines, each with a name relative to the test
		// directory.
		want []string
		// The status the run exits with.
		exit int
	}{
		"explicit names": {
			files: map[string]string{"a.yaml": "name: a\n", "b.yaml": "name: b\n"},
			args:  []string{"a.yaml", "b.yaml"},
			want:  []string{"a.yaml: valid", "b.yaml: valid"},
		},
		"control characters in a matched name": {
			files: map[string]string{"\x1b]52;c;eA==\a.yaml": "name: a\n"},
			args:  []string{"*.yaml"},
			// The Control Pictures for ESC and BEL stand in for them.
			want: []string{"␛]52;c;eA==␇.yaml: valid"},
		},
		"every result": {
			files: map[string]string{
				"good.yaml":  "name: a\n",
				"bad.yaml":   "value: 1\n",
				"dep.yaml":   stringtest.LinesLF("name: a", "old: 1"),
				"deps.yaml":  stringtest.LinesLF("name: a", "old: 1", "---", "name: b", "old: 2"),
				"other.yaml": "name: [\n",
			},
			args: []string{"good.yaml", "bad.yaml", "missing.yaml", "dep.yaml", "deps.yaml", "other.yaml"},
			want: []string{
				"good.yaml: valid",
				"bad.yaml: invalid",
				"missing.yaml: failed",
				"dep.yaml: valid, 1 warning",
				"deps.yaml: valid, 2 warnings",
				"other.yaml: invalid",
			},
			exit: exitFailure,
		},
		"strict fails a file on its warnings": {
			files: map[string]string{
				"good.yaml": "name: a\n",
				"dep.yaml":  stringtest.LinesLF("name: a", "old: 1"),
			},
			args: []string{"--strict", "good.yaml", "dep.yaml"},
			want: []string{"good.yaml: valid", "dep.yaml: invalid"},
			exit: exitInvalid,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			schemaPath := yamltest.WriteFile(t, dir, "schema.json", oldSchema)

			for file, content := range tc.files {
				writeOrSkip(t, dir, file, content)
			}

			args := []string{"--schema", schemaPath}

			for _, arg := range tc.args {
				if !strings.HasPrefix(arg, "-") {
					arg = filepath.Join(dir, arg)
				}

				args = append(args, arg)
			}

			var want strings.Builder

			for _, w := range tc.want {
				want.WriteString(filepath.Join(dir, w) + "\n")
			}

			out := &bytes.Buffer{}

			err := quietValidateCmd(out, io.Discard, args...).Execute()
			assert.Equal(t, tc.exit, exitCode(err), "%v", err)
			assert.Equal(t, want.String(), out.String())
			assert.NotContains(t, out.String(), "\x1b")
		})
	}
}

func TestValidateCmdErrorNames(t *testing.T) {
	t.Parallel()

	// Every error names a matched file as escape.Line draws it, as the
	// library names a source. The error handler keeps each line break in
	// a message as a row break, so a raw newline in this name would start
	// a row that reads as a branch for a file named "other.yaml". The tab
	// reads as spaces in every error, as it does in a result line.
	const file = "evil\t\n└── other.yaml"

	tcs := map[string]struct {
		err error
		// Contents of the file. With none, the file is a dangling
		// symlink, as a glob can match, so its read fails.
		content string
		// The result that the result line reports, or none when every
		// write to stdout fails.
		result string
		// The status the run exits with.
		exit int
		// Makes every write to stdout fail.
		failOut bool
		// Makes every write to stderr fail.
		failErrOut bool
	}{
		"invalid document": {content: "value: 1\n", result: "invalid", exit: exitInvalid},
		"unreadable file":  {err: fs.ErrNotExist, result: "failed", exit: exitFailure},
		"result line not written": {
			content: "name: a\n",
			failOut: true,
			err:     io.ErrClosedPipe,
			exit:    exitFailure,
		},
		"warnings not written": {
			content:    stringtest.LinesLF("name: a", "old: 1"),
			failErrOut: true,
			result:     "valid, 1 warning",
			err:        io.ErrClosedPipe,
			exit:       exitFailure,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			schemaPath := yamltest.WriteFile(t, dir, "schema.json", oldSchema)

			path := filepath.Join(dir, file)

			if tc.content == "" {
				yamltest.SymlinkOrSkip(t, filepath.Join(dir, "missing.yaml"), path)
			} else {
				writeOrSkip(t, dir, file, tc.content)
			}

			buf := &bytes.Buffer{}

			out, errOut := io.Writer(buf), io.Discard
			if tc.failOut {
				out = closedWriter{}
			}

			if tc.failErrOut {
				errOut = closedWriter{}
			}

			err := quietValidateCmd(out, errOut, "--schema", schemaPath, filepath.Join(dir, "evil*")).Execute()
			require.Error(t, err)
			assert.Equal(t, tc.exit, exitCode(err), err.Error())

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}

			assert.Equal(t, 1, strings.Count(err.Error(), escape.Line(path)), err.Error())

			if tc.result != "" {
				assert.Equal(t, escape.Line(path)+": "+tc.result+"\n", buf.String())
			}
		})
	}
}

func TestValidateCmdDetaches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	schemaPath := yamltest.WriteFile(t, dir, "schema.json", string(nameSchema))
	paths := []string{
		yamltest.WriteFile(t, dir, "first.yaml", stringtest.LinesLF("value: 1", "---", "name: b")),
		yamltest.WriteFile(t, dir, "second.yaml", "name: [\n"),
	}

	err := quietValidateCmd(io.Discard, io.Discard, append([]string{"--schema", schemaPath}, paths...)...).Execute()
	require.Error(t, err)

	reg, regErr := buildRegistry(t.Context(), schemaPath, schema.Unrestricted, nil)
	require.NoError(t, regErr)

	// The errors that validateFile returns hold their sources. The
	// bindings of the error of the run report no Source, and that error
	// renders as those errors do.
	live := make([]error, 0, len(paths))

	for _, path := range paths {
		warnings := niceyaml.NewWarnings()
		live = append(live, warnings.Report(validateFile(warnings.Context(t.Context()), path, reg)))
	}

	want := errors.Join(live...)

	assert.Equal(t, niceyaml.FormatError(want), niceyaml.FormatError(err))
	assert.Equal(t, exitCode(want), exitCode(err))

	var names []string

	for bound := range niceyaml.Bindings(err) {
		assert.Nil(t, bound.Source())

		names = append(names, bound.Name())
	}

	assert.Equal(t, paths, names)
}

// closedWriter fails every write as a closed pipe does, so tests can
// reach the error a command reports when it cannot write its output.
type closedWriter struct{}

func (closedWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestValidateCmdDeprecations(t *testing.T) {
	t.Parallel()

	// A file reports its result on stdout and its warnings on stderr,
	// whether it passes or fails.
	tcs := map[string]struct {
		input string
		// Flags before the file argument.
		args []string
		// The stdout and the stderr of the run, and the error it fails
		// with, or none for a run that passes.
		out    string
		errOut string
		err    string
	}{
		"a file that passes prints its warnings on stderr": {
			input: stringtest.LinesLF(`version: "3.8"`, "services: {}"),
			out:   "compose.yaml: valid, 1 warning\n",
			errOut: stringtest.LinesLF(
				`compose.yaml:1:1: warning: $.version~: deprecated field "version"`,
				"`-- Compose ignores version.",
				"",
				`   1 | version: "3.8"`,
				"     | ^^^^^^^",
				"   2 | services: {}",
				"",
			),
		},
		"a file that fails reports its warnings among its errors": {
			input: stringtest.LinesLF(`version: "3.8"`, "services: 1"),
			out:   "compose.yaml: invalid\n",
			err: stringtest.JoinLF(
				"compose.yaml: 1 error, 1 warning",
				`compose.yaml:1:1: warning: $.version~: deprecated field "version"`,
				`compose.yaml:2:11: $.services: expected "object", got "integer"`,
			),
		},
		"strict reports the warnings of a file as errors": {
			input: stringtest.LinesLF(`version: "3.8"`, "services: {}"),
			args:  []string{"--strict"},
			out:   "compose.yaml: invalid\n",
			err:   `compose.yaml:1:1: $.version~: deprecated field "version"`,
		},
		"strict counts the warnings of a file that fails as errors": {
			input: stringtest.LinesLF(`version: "3.8"`, "services: 1"),
			args:  []string{"--strict"},
			out:   "compose.yaml: invalid\n",
			err: stringtest.JoinLF(
				"compose.yaml: 2 errors",
				`compose.yaml:1:1: $.version~: deprecated field "version"`,
				`compose.yaml:2:11: $.services: expected "object", got "integer"`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			yamltest.WriteFile(t, dir, "compose.schema.json", `{"properties": {`+
				`"version": {"deprecated": true, "deprecationMessage": "Compose ignores version."}, `+
				`"services": {"type": "object"}}}`)

			path := yamltest.WriteFile(t, dir, "compose.yaml", tc.input)

			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

			args := append([]string{"--schema", filepath.Join(dir, "compose.schema.json")}, tc.args...)

			err := quietValidateCmd(out, errOut, append(args, path)...).Execute()
			assert.Equal(t, tc.out, strings.ReplaceAll(out.String(), path, "compose.yaml"))
			assert.Equal(t, tc.errOut, strings.ReplaceAll(errOut.String(), path, "compose.yaml"))

			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, strings.ReplaceAll(err.Error(), path, "compose.yaml"))
				assert.Equal(t, exitInvalid, exitCode(err))

				return
			}

			require.NoError(t, err)
		})
	}
}
