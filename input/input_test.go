package input_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/input"
)

func TestInput_Files(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree{
		files: map[string]string{
			"data/bad.yaml":        "a: [1\n",
			"data/cafe.yaml":       cafeDoc,
			"data/.github/ci.yaml": "on: push\n",
			"data/notes.txt":       "not selected\n",
			"shared/hours.yaml":    cafeDoc,
		},
		links: map[string]string{"data/out.yaml": "../shared/hours.yaml"},
	}.write(t, dir)

	data := filepath.Join(dir, "data")

	files, err := input.Path(data, input.WithRecursive(true), input.WithHidden(true)).Files(t.Context())
	require.NoError(t, err)

	var got []string

	for _, f := range files {
		name, err := filepath.Rel(dir, f.Name)
		require.NoError(t, err)

		name = filepath.ToSlash(name)

		switch {
		case f.Err != nil:
			assert.Nil(t, f.Source, name)
			require.ErrorIs(t, f.Err, input.ErrOutsideLink, name)

			got = append(got, name+": error")

		default:
			require.NotNil(t, f.Source, name)
			assert.Equal(t, f.Name, f.Source.Name())

			// A file that does not parse is a Source all the same. The
			// problem shows in the parse.
			_, parseErr := f.Source.File()

			result := "ok"
			if parseErr != nil {
				result = "syntax"
			}

			got = append(got, name+": "+result)
		}
	}

	assert.Equal(t, []string{
		"data/.github/ci.yaml: ok",
		"data/bad.yaml: syntax",
		"data/cafe.yaml: ok",
		"data/out.yaml: error",
	}, got)
}

func TestInput_Files_Fails(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	tcs := map[string]struct {
		ctx context.Context
		in  input.Input
		is  error
	}{
		"bad pattern": {
			in: input.FS(mapFS(nil), ".", input.WithExclude("[")),
			is: input.ErrInvalidPattern,
		},
		"missing input": {
			in: input.FS(mapFS(nil), "config"),
			is: fs.ErrNotExist,
		},
		"canceled": {
			ctx: canceled,
			in:  input.Sources(niceyaml.NewSourceFromString(cafeDoc)),
			is:  context.Canceled,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := tc.ctx
			if ctx == nil {
				ctx = t.Context()
			}

			files, err := tc.in.Files(ctx)
			require.ErrorIs(t, err, tc.is)
			assert.Nil(t, files)
		})
	}
}

func TestInput_Files_Zero(t *testing.T) {
	t.Parallel()

	files, err := input.Input{}.Files(t.Context())
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestInput_Files_EmptyDirectory(t *testing.T) {
	t.Parallel()

	files, err := input.FS(mapFS(map[string]string{"notes.txt": ""}), "config").Files(t.Context())
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestSources(t *testing.T) {
	t.Parallel()

	a := niceyaml.NewSourceFromString(cafeDoc, niceyaml.WithName("a.yaml"))
	b := niceyaml.NewSourceFromString(cafeDoc, niceyaml.WithName("b.yaml"))

	files, err := readAll(t, input.Join(input.Sources(a, nil, b), input.Sources(a)))
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Same(t, a, files[0].Source)
	assert.Same(t, b, files[1].Source)
}

func TestJoin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree{
		files: map[string]string{"config/cafe.yaml": cafeDoc, "config/hours.yaml": cafeDoc},
		links: map[string]string{"cafe.yaml": "config/cafe.yaml"},
	}.write(t, dir)

	// A file that several inputs reach reads once, at its first place.
	files, err := readAll(t, input.Join(
		input.Path(filepath.Join(dir, "cafe.yaml")),
		input.Path(filepath.Join(dir, "config")),
	))
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.ToSlash(filepath.Join(dir, "cafe.yaml")),
		filepath.ToSlash(filepath.Join(dir, "config", "hours.yaml")),
	}, readNames(files))
}

// failingReader is an [io.Reader] whose first read fails with err, and
// whose later reads find the end of the text.
type failingReader struct {
	err  error
	done bool
}

// Read returns the error of r on the first call, and [io.EOF] after.
func (r *failingReader) Read([]byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}

	r.done = true

	return 0, r.err
}

func TestReader(t *testing.T) {
	t.Parallel()

	errBroken := errors.New("broken pipe")

	tcs := map[string]struct {
		in   input.Input
		want []string
		err  string
		is   []error
	}{
		"text": {
			in:   input.Reader("cafe.yaml", strings.NewReader(cafeDoc)),
			want: []string{"cafe.yaml"},
		},
		"name option": {
			in: input.Reader("cafe.yaml", strings.NewReader(cafeDoc),
				input.WithSourceOptions(niceyaml.WithName("ignored"))),
			want: []string{"cafe.yaml"},
		},
		"one reader twice": {
			in: func() input.Input {
				in := input.Reader("cafe.yaml", strings.NewReader(cafeDoc))

				return input.Join(in, in)
			}(),
			want: []string{"cafe.yaml"},
		},
		"read that fails": {
			in:  input.Reader("cafe.yaml", &failingReader{err: errBroken}),
			err: "cafe.yaml: read: broken pipe",
			is:  []error{errBroken},
		},
		"byte limit": {
			in: input.Reader("cafe.yaml", strings.NewReader(cafeDoc),
				input.WithSourceOptions(niceyaml.WithByteLimit(10), niceyaml.WithName("ignored"))),
			err: "cafe.yaml: file is larger than 10 bytes",
			is:  []error{niceyaml.ErrLimit},
		},
		"bad pattern": {
			in:  input.Reader("cafe.yaml", strings.NewReader(cafeDoc), input.WithExclude("[")),
			err: `pattern "["`,
			is:  []error{input.ErrInvalidPattern},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Every read of the Input gives the result of the first read.
			for range 2 {
				files, err := readAll(t, tc.in)
				if tc.err != "" {
					require.Error(t, err)

					for _, target := range tc.is {
						require.ErrorIs(t, err, target)
					}

					assert.Contains(t, formatAll(err), tc.err)

					continue
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, readNames(files))
				assert.Equal(t, map[string]string{"cafe.yaml": cafeDoc}, readTexts(files))
			}
		})
	}
}

// panicReader is an [io.Reader] whose reads panic.
type panicReader struct{}

// Read panics.
func (panicReader) Read([]byte) (int, error) {
	panic("reader broke")
}

func TestReader_Panic(t *testing.T) {
	t.Parallel()

	in := input.Reader("cafe.yaml", panicReader{})

	// Every read of the Input repeats the panic of the first read.
	for range 2 {
		assert.PanicsWithValue(t, "reader broke", func() {
			_, _ = in.Files(t.Context()) //nolint:errcheck // The read panics.
		})
	}
}

func TestWithSourceOptions(t *testing.T) {
	t.Parallel()

	files, err := readAll(t, input.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc}), "config",
		input.WithSourceOptions(niceyaml.WithByteLimit(10), niceyaml.WithName("ignored"))))
	require.ErrorIs(t, err, niceyaml.ErrLimit)
	assert.True(t, niceyaml.IsInvalid(err))
	assert.Empty(t, readNames(files))
	assert.Contains(t, formatAll(err), "config/cafe.yaml: file is larger than 10 bytes")
}

func ExampleInput_Files() {
	fsys := fstest.MapFS{
		"config/cafe.yaml":        {Data: []byte("name: yamull\n")},
		"config/broken.yaml":      {Data: []byte("a: [1\n")},
		"config/.github/ci.yaml":  {Data: []byte("on: push\n")},
		"config/notes/readme.txt": {Data: []byte("not YAML\n")},
	}

	in := input.FS(fsys, "config", input.WithRecursive(true), input.WithHidden(true))

	files, err := in.Files(context.Background())
	if err != nil {
		fmt.Println(err) // A bad pattern, a missing directory, or the end of ctx.

		return
	}

	for _, f := range files {
		if f.Err != nil {
			fmt.Printf("%s: cannot read\n", f.Name)

			continue
		}

		_, parseErr := f.Source.File()
		fmt.Printf("%s: parses=%t\n", f.Name, parseErr == nil)
	}

	// Output:
	// config/.github/ci.yaml: parses=true
	// config/broken.yaml: parses=false
	// config/cafe.yaml: parses=true
}
