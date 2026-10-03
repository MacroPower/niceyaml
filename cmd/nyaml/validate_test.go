package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/schema"
)

// nameSchema is the schema the validateFile tests apply to every document
// of their fixtures. It requires a "name" key.
var nameSchema = []byte(`{
	"type": "object",
	"properties": {"name": {"type": "string"}},
	"required": ["name"]
}`)

func TestValidateFile(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		// Positions of every document the joined error reports, in file
		// order, each as the "line:col:" that follows the file path. Valid
		// documents have no entry, and a valid file has none.
		want []string
	}{
		"every document valid": {
			content: "name: a\n---\nname: b\n---\nname: c\n",
		},
		"first and last document invalid": {
			content: "value: 1\n---\nname: b\n---\nvalue: 3\n",
			want:    []string{"1:1:", "5:1:"},
		},
		"first two documents invalid": {
			content: "value: 1\n---\nvalue: 2\n---\nname: c\n",
			want:    []string{"1:1:", "3:1:"},
		},
		"middle document invalid": {
			content: "name: a\n---\nvalue: 2\n---\nname: c\n",
			want:    []string{"3:1:"},
		},
		"comment above the first header": {
			content: "# yaml-language-server: $schema=./s.json\n---\nname: a\n",
		},
		"comment above an invalid document": {
			content: "# a preamble\n---\nvalue: 1\n",
			want:    []string{"3:1:"},
		},
		"invalid document after consecutive headers": {
			content: "name: a\n---\n---\nvalue: 1\n",
			want:    []string{"2:1:", "4:1:"},
		},
		"only document does not parse": {
			content: "name: [\n",
			want:    []string{"1:7:"},
		},
		"syntax errors beside violations": {
			content: "name: a\n---\nname: [\n---\nvalue: 3\n---\nname: @x\n---\nvalue: 5\n",
			want:    []string{"3:7:", "5:1:", "7:7:", "9:1:"},
		},
		"valid documents around one that does not parse": {
			content: "name: a\n---\nname: [\n---\nname: c\n",
			want:    []string{"3:7:"},
		},
		// The header parses together with the document above it, so both
		// documents carry the one error, and the file reports it once.
		"header after an anchor with no value": {
			content: "name: &x\n---\nname: [\n---\nvalue: 5\n",
			want:    []string{"3:7:", "5:1:"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))

			reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(nameSchema)))

			err := validateFile(t.Context(), path, reg)
			if len(tc.want) == 0 {
				require.NoError(t, err)

				return
			}

			var joined interface{ Unwrap() []error }

			require.ErrorAs(t, err, &joined)

			var got []string

			for _, e := range joined.Unwrap() {
				msg, ok := strings.CutPrefix(e.Error(), path+":")
				require.True(t, ok, e.Error())

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
			reg:     schema.NewRegistry(schema.WithResolvers(schema.Embedded(nameSchema))),
			content: "",
			want:    `: $: expected "object", got "null"`,
		},
		"document end marker against a schema": {
			reg:     schema.NewRegistry(schema.WithResolvers(schema.Embedded(nameSchema))),
			content: "...\n",
			want:    `: $: expected "object", got "null"`,
		},
		"document end marker with no schema": {
			reg: schema.NewRegistry(
				schema.WithResolvers(schema.Directive()),
				schema.WithRequireSchema(false),
			),
			content: "...\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))

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
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("value: 1\n"), 0o600))

	wd, err := os.Getwd()
	require.NoError(t, err)

	rel, err := filepath.Rel(wd, path)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(rel))

	var got string

	reg := schema.NewRegistry(schema.WithResolvers(
		schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			got = doc.FilePath()

			return schema.Embedded(nameSchema), nil
		}),
	))

	err = validateFile(t.Context(), rel, reg)
	require.Error(t, err)
	assert.Equal(t, path, got)
	assert.Contains(t, err.Error(), rel+":1:1: ")
}

func TestValidateFileDotDotAfterSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	// With ldir linking to sub/deep, the OS reads ldir/../x.yaml as
	// sub/x.yaml, so the directive's ./s.json names sub/s.json.
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")

	require.NoError(t, os.MkdirAll(filepath.Join(sub, "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "s.json"), nameSchema, 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "x.yaml"),
		[]byte("# yaml-language-server: $schema=./s.json\nname: a\n"),
		0o600,
	))

	err := os.Symlink(filepath.Join("sub", "deep"), filepath.Join(dir, "ldir"))
	if err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	// Joining the name with filepath.Join would clean it to x.yaml.
	path := dir + "/ldir/../x.yaml"

	reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	require.NoError(t, validateFile(t.Context(), path, reg))
}

func TestBuildRegistrySchemaDotDotAfterSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	// With ldir linking to sub/deep, the OS reads ldir/../schema.json as
	// sub/schema.json, which accepts a string. The schema.json beside
	// ldir, which cleaning the path as text would name, rejects one.
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")

	require.NoError(t, os.MkdirAll(filepath.Join(sub, "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "schema.json"), []byte(`{"type": "string"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type": "integer"}`), 0o600))

	yamlPath := filepath.Join(dir, "x.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("hello\n"), 0o600))

	err := os.Symlink(filepath.Join("sub", "deep"), filepath.Join(dir, "ldir"))
	if err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	wd, err := os.Getwd()
	require.NoError(t, err)

	rel, err := filepath.Rel(wd, dir)
	require.NoError(t, err)

	// The test builds each ref as text, since filepath.Join would clean it.
	tcs := map[string]struct {
		ref string
	}{
		"absolute": {
			ref: dir + "/ldir/../schema.json",
		},
		"relative": {
			ref: rel + "/ldir/../schema.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := buildRegistry(t.Context(), tc.ref)
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	// Every link leads to sub/deep, so the OS reads each ref as
	// sub/schema.json, which accepts a string. Cleaning a ref as text
	// would name a schema.json beside the link, which rejects one or
	// does not exist.
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")

	require.NoError(t, os.MkdirAll(filepath.Join(sub, "deep"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cfg:v2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "file:"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "schema.json"), []byte(`{"type": "string"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type": "integer"}`), 0o600))

	yamlPath := filepath.Join(dir, "x.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("hello\n"), 0o600))

	err := os.Symlink(filepath.Join("sub", "deep"), filepath.Join(dir, "v1:ldir"))
	if err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	require.NoError(t, os.Symlink(filepath.Join("..", "sub", "deep"), filepath.Join(dir, "cfg:v2", "ldir")))
	require.NoError(t, os.Symlink(filepath.Join("..", "sub", "deep"), filepath.Join(dir, "file:", "ldir")))

	t.Chdir(dir)

	tcs := map[string]struct {
		ref string
	}{
		"colon in the symlink name": {
			ref: "v1:ldir/../schema.json",
		},
		"colon in a directory before the symlink": {
			ref: "cfg:v2/ldir/../schema.json",
		},
		// FileOrURL reads a file URL whose host is not localhost as a
		// relative path, here file:/ldir/../schema.json.
		"file URL with a host": {
			ref: "file://ldir/../schema.json",
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			reg, err := buildRegistry(t.Context(), tc.ref)
			require.NoError(t, err)

			require.NoError(t, validateFile(t.Context(), yamlPath, reg))
		})
	}
}

func TestReadsAsPath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		ref  string
		want bool
	}{
		"https URL":                    {ref: "https://example.com/schema.json"},
		"upper-case http URL":          {ref: "HTTP://example.com/schema.json"},
		"file URL":                     {ref: "file:///tmp/schema.json"},
		"upper-case file URL":          {ref: "FILE:/tmp/schema.json"},
		"file URL with localhost":      {ref: "file://localhost/tmp/schema.json"},
		"file URL with a host":         {ref: "file://example.com/dir/schema.json", want: true},
		"file URL that does not parse": {ref: "file:/%zz/schema.json", want: true},
		"drive with a slash":           {ref: "C:/schemas/schema.json"},
		"drive with a backslash":       {ref: `c:\schemas\schema.json`},
		"bare drive":                   {ref: "D:"},
		"rooted path":                  {ref: "/schemas/schema.json", want: true},
		"relative path":                {ref: "schemas/schema.json", want: true},
		"colon in the first element":   {ref: "v1:dir/schema.json", want: true},
		"single letter and colon":      {ref: "C:schema.json", want: true},
		"other scheme":                 {ref: "urn:schema", want: true},
		"empty":                        {ref: ""},
		// Windows reads any character before a colon as a drive, so there
		// the ref reads as absolute.
		"digit and colon": {ref: "1:/schema.json", want: runtime.GOOS != "windows"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, readsAsPath(tc.ref))
		})
	}
}

func TestPhysicalAbs(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	// Create a directory structure with a symlink:
	// dir/
	//   sub/
	//     deep/
	//   ldir -> sub/deep
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o755))

	err := os.Symlink(filepath.Join("sub", "deep"), filepath.Join(dir, "ldir"))
	if err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	// Resolving the part up to a ".." also resolves any symlink in the
	// temporary directory itself.
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)

	rel, err := filepath.Rel(wd, dir)
	require.NoError(t, err)

	// The test builds each path as text, since filepath.Join would clean it.
	tcs := map[string]struct {
		path string
		want string
	}{
		"no dot-dot keeps a symlinked name": {
			path: dir + "/ldir/x.yaml",
			want: filepath.Join(dir, "ldir", "x.yaml"),
		},
		"dot-dot after a symlinked directory": {
			path: dir + "/ldir/../x.yaml",
			want: filepath.Join(realDir, "sub", "x.yaml"),
		},
		"dot-dot after a plain directory": {
			path: dir + "/sub/../x.yaml",
			want: filepath.Join(realDir, "x.yaml"),
		},
		"symlinked name after the last dot-dot": {
			path: dir + "/sub/../ldir/x.yaml",
			want: filepath.Join(realDir, "ldir", "x.yaml"),
		},
		"relative dot-dot after a symlinked directory": {
			path: rel + "/ldir/../x.yaml",
			want: filepath.Join(realDir, "sub", "x.yaml"),
		},
		"dot-dot after a missing directory": {
			path: dir + "/missing/../x.yaml",
			want: filepath.Join(dir, "x.yaml"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, physicalAbs(tc.path))
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
		"missing file": {
			err: schema.ErrLoad,
		},
		"invalid JSON": {
			content: `{"type": 12`,
			err:     schema.ErrCompile,
		},
		"URL fails": {
			url: true,
			err: schema.ErrLoad,
		},
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
				require.NoError(t, os.WriteFile(ref, []byte(tc.content), 0o600))
			}

			// Three documents across two files, so a schema loaded per
			// document would report three times.
			yamlPaths := []string{filepath.Join(dir, "one.yaml"), filepath.Join(dir, "two.yaml")}
			require.NoError(t, os.WriteFile(yamlPaths[0], []byte("a: 1\n"), 0o600))
			require.NoError(t, os.WriteFile(yamlPaths[1], []byte("a: 1\n---\nb: 2\n"), 0o600))

			out := &bytes.Buffer{}

			cmd := validateCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetArgs(append([]string{"--schema", ref}, yamlPaths...))

			err := cmd.Execute()
			require.ErrorIs(t, err, tc.err)

			msg := err.Error()
			assert.True(t, strings.HasPrefix(msg, "--schema: "), msg)
			assert.Equal(t, 1, strings.Count(msg, tc.err.Error()), msg)

			for _, yamlPath := range yamlPaths {
				assert.NotContains(t, msg, yamlPath)
			}

			assert.Empty(t, out.String())

			if tc.url {
				assert.Equal(t, int32(1), fetches.Load())
			}
		})
	}
}

func TestValidateCmdHelp(t *testing.T) {
	t.Parallel()

	// The help names every source of a document's schema, so a reader
	// learns that a run without --schema can still download one.
	long := validateCmd().Long
	for _, want := range []string{"--schema", "$schema=", "$schema=none", "SchemaStore"} {
		assert.Contains(t, long, want)
	}
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

				doc = "# yaml-language-server: $schema=" + srv.URL + "/s.json\n" + doc
			} else {
				cancel()
			}

			body := strings.Join([]string{doc, doc, doc}, "---\n")
			dir := t.TempDir()

			var args []string

			for _, name := range []string{"a.yaml", "b.yaml", "c.yaml"} {
				path := filepath.Join(dir, name)
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

				args = append(args, path)
			}

			out := &bytes.Buffer{}

			cmd := validateCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetArgs(args)

			err := cmd.ExecuteContext(ctx)
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, 1, strings.Count(err.Error(), context.Canceled.Error()), err.Error())
			assert.Empty(t, out.String())
		})
	}
}

func TestValidateCmdOutput(t *testing.T) {
	t.Parallel()

	// The per-file line goes to the writer the caller set on the command,
	// so embedding the command and redirecting its output captures it.
	tcs := map[string]struct {
		// Names of the valid files to write to the test directory.
		files []string
		// Arguments after the schema flag, relative to the test directory.
		args []string
		// Names that the "valid" lines report in order, relative to the
		// test directory.
		want []string
	}{
		"explicit names": {
			files: []string{"a.yaml", "b.yaml"},
			args:  []string{"a.yaml", "b.yaml"},
			want:  []string{"a.yaml", "b.yaml"},
		},
		"control characters in a matched name": {
			files: []string{"\x1b]52;c;eA==\a.yaml"},
			args:  []string{"*.yaml"},
			// The Control Pictures for ESC and BEL stand in for them.
			want: []string{"␛]52;c;eA==␇.yaml"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			schemaPath := filepath.Join(dir, "schema.json")
			require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type": "object"}`), 0o600))

			for _, file := range tc.files {
				err := os.WriteFile(filepath.Join(dir, file), []byte("name: a\n"), 0o600)
				if err != nil {
					// Some file systems, such as those on Windows, reject
					// control characters in a name.
					t.Skipf("write %q: %v", file, err)
				}
			}

			args := []string{"--schema", schemaPath}
			for _, arg := range tc.args {
				args = append(args, filepath.Join(dir, arg))
			}

			var want strings.Builder

			for _, w := range tc.want {
				want.WriteString(filepath.Join(dir, w) + ": valid\n")
			}

			out := &bytes.Buffer{}

			cmd := validateCmd()
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetArgs(args)

			require.NoError(t, cmd.Execute())
			assert.Equal(t, want.String(), out.String())
			assert.NotContains(t, out.String(), "\x1b")
		})
	}
}

func TestValidateCmdErrorNames(t *testing.T) {
	t.Parallel()

	// Every error names a matched file with its control characters
	// escaped. The error handler keeps each line break in a message as a
	// row break, so a raw newline in this name would start a row that
	// reads as a branch for a file named "other.yaml".
	const file = "evil\n└── other.yaml"

	tcs := map[string]struct {
		err error
		// Contents of the file. With none, the file is a dangling
		// symlink, as a glob can match, so its read fails.
		content string
		// Makes every write of a result line fail.
		failWrite bool
	}{
		"invalid document": {
			content: "value: 1\n",
		},
		"unreadable file": {
			err: fs.ErrNotExist,
		},
		"result line not written": {
			content:   "name: a\n",
			failWrite: true,
			err:       io.ErrClosedPipe,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			schemaPath := filepath.Join(dir, "schema.json")
			require.NoError(t, os.WriteFile(schemaPath, nameSchema, 0o600))

			path := filepath.Join(dir, file)

			if tc.content == "" {
				err := os.Symlink(filepath.Join(dir, "missing.yaml"), path)
				if err != nil {
					t.Skipf("symlink %q: %v", file, err)
				}
			} else {
				err := os.WriteFile(path, []byte(tc.content), 0o600)
				if err != nil {
					// Some file systems, such as those on Windows, reject
					// control characters in a name.
					t.Skipf("write %q: %v", file, err)
				}
			}

			out := io.Writer(&bytes.Buffer{})
			if tc.failWrite {
				out = closedWriter{}
			}

			cmd := validateCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--schema", schemaPath, filepath.Join(dir, "evil*")})

			err := cmd.Execute()
			require.Error(t, err)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}

			assert.Equal(t, 1, strings.Count(err.Error(), escape.Control(path)), err.Error())
		})
	}
}

// closedWriter fails every write as a closed pipe does, so tests can
// reach the error a command reports when it cannot write its output.
type closedWriter struct{}

func (closedWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
