package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// The errors the unmarshalers of this file report.
var (
	errBadServer    = errors.New("bad server")
	errRepoRequired = errors.New("repo is required")
	errPortRequired = errors.New("port is required")
)

// problemServer is an element of [problemConfig].
type problemServer struct {
	Name    string        `yaml:"name"`
	Port    int           `yaml:"port"`
	Timeout time.Duration `yaml:"timeout"`
}

// problemConfig is the target of a document that holds several problems.
// The decoder reads its fields in the order below, so it rejects a label
// before a server and a server before the replicas.
type problemConfig struct {
	Labels   map[string]int  `yaml:"labels"`
	Servers  []problemServer `yaml:"servers"`
	Replicas int             `yaml:"replicas"`
	Timeout  int             `yaml:"timeout"`
}

// problemImage decodes itself through a type whose fields do not mirror
// its own, and requires a repo. Its document writes pull as text, where
// the field holds a boolean.
type problemImage struct {
	Repo string
	Pull bool
}

func (i *problemImage) UnmarshalYAML(unmarshal func(any) error) error {
	var raw struct {
		Repo string `yaml:"repo"`
		Pull string `yaml:"pull"`
	}

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	if raw.Repo == "" {
		return errRepoRequired
	}

	i.Repo, i.Pull = raw.Repo, raw.Pull == "always"

	return nil
}

// problemPulled has the fields of [problemImage] and no method. A
// yaml.CustomUnmarshaler option decodes it.
type problemPulled struct {
	Repo string `yaml:"repo"`
	Pull bool   `yaml:"pull"`
}

// decodePulled decodes a [problemPulled] as [problemImage] decodes
// itself.
func decodePulled(p *problemPulled, data []byte) error {
	var raw struct {
		Repo string `yaml:"repo"`
		Pull string `yaml:"pull"`
	}

	err := yaml.Unmarshal(data, &raw)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}

	if raw.Repo == "" {
		return errRepoRequired
	}

	p.Repo, p.Pull = raw.Repo, raw.Pull == "always"

	return nil
}

// problemWrapped decodes itself through a second type with the same
// fields and wraps the error of that decode in errBadServer.
type problemWrapped struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
}

func (w *problemWrapped) UnmarshalYAML(unmarshal func(any) error) error {
	type plain problemWrapped

	err := unmarshal((*plain)(w))
	if err != nil {
		return fmt.Errorf("%w: %w", errBadServer, err)
	}

	return nil
}

// problemPorted decodes itself over the value it holds and requires a
// port, which a caller may set before the decode.
type problemPorted struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func (p *problemPorted) UnmarshalYAML(unmarshal func(any) error) error {
	type plain problemPorted

	err := unmarshal((*plain)(p))
	if err != nil {
		return err
	}

	if p.Port == 0 {
		return errPortRequired
	}

	return nil
}

// portValidator is a [yaml.StructValidator] that requires the port of a
// [problemServer].
type portValidator struct{}

func (portValidator) Struct(v any) error {
	if s, ok := v.(problemServer); ok && s.Port == 0 {
		return errPortRequired
	}

	return nil
}

// callsKey is the context key of the [*problemCalls] of a decode.
type callsKey struct{}

// problemCalls is the state the unmarshalers of one decode share: the
// names [problemUnique] has seen, the calls [problemCounted] took, and a
// function that ends the context of the decode.
type problemCalls struct {
	seen   map[string]bool
	cancel context.CancelFunc
	count  int
}

// problemUnique decodes itself from text and rejects a name another
// value of the decode holds.
type problemUnique string

func (u *problemUnique) UnmarshalYAML(ctx context.Context, unmarshal func(any) error) error {
	var name string

	err := unmarshal(&name)
	if err != nil {
		return err
	}

	calls, ok := ctx.Value(callsKey{}).(*problemCalls)
	if !ok {
		return nil
	}

	if calls.seen[name] {
		return fmt.Errorf("duplicate name %q", name)
	}

	calls.seen[name] = true
	*u = problemUnique(name)

	return nil
}

// problemCounted decodes itself from any mapping and counts its calls.
type problemCounted struct {
	Name string
}

func (c *problemCounted) UnmarshalYAML(ctx context.Context, unmarshal func(any) error) error {
	if calls, ok := ctx.Value(callsKey{}).(*problemCalls); ok {
		calls.count++
	}

	var raw struct {
		Name string `yaml:"name"`
	}

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	c.Name = raw.Name

	return nil
}

// problemEnding decodes itself from any value and ends the context of
// the decode.
type problemEnding struct{}

func (*problemEnding) UnmarshalYAML(ctx context.Context, _ func(any) error) error {
	if calls, ok := ctx.Value(callsKey{}).(*problemCalls); ok {
		calls.cancel()
	}

	return nil
}

// problemNested decodes itself with a decode of its text as a source of
// its own, which rejects unknown fields, and returns the error of that
// decode as that source bound it.
type problemNested struct{}

func (*problemNested) UnmarshalYAML(ctx context.Context, data []byte) error {
	source := niceyaml.NewSourceFromString(string(data), niceyaml.WithName("inner.yaml"))

	_, err := source.Decode[struct {
		Port int `yaml:"port"`
	}](ctx, niceyaml.WithDisallowUnknownFields(true))

	return err
}

// problemDeep holds itself, for a document nested many levels deep.
type problemDeep struct {
	C *problemDeep `yaml:"c"`
	V int          `yaml:"v"`
}

// deepDocument returns a flow mapping nested depth levels deep, whose
// innermost mapping holds a value no integer field takes.
func deepDocument(depth int) string {
	return strings.Repeat("{c: ", depth) + "{v: bad}" + strings.Repeat("}", depth) + "\n"
}

// requireInvalid checks that the document is at fault for err, the error
// of a decode, and for each of its problems, of which it holds count.
func requireInvalid(t *testing.T, err error, count int) {
	t.Helper()

	require.True(t, niceyaml.IsInvalid(err))

	found := 0

	for problem := range niceyaml.NewErrorTree(err).Problems() {
		require.True(t, problem.Invalid(), problem.Text)

		found++
	}

	require.Equal(t, count, found)
}

func TestSource_Decode_Problems(t *testing.T) {
	t.Parallel()

	// A user fixed one report for each run of this document: the timeout,
	// then the port, then the unknown field.
	source := niceyaml.NewSourceFromString(
		"replcas: 3\ntimeout: soon\nservers:\n  - port: http\n  - port: 80\n",
		niceyaml.WithName("app.yaml"),
	)

	_, err := source.Decode[problemConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
	require.EqualError(t, err, stringtest.JoinLF(
		"app.yaml: 3 problems",
		`app.yaml:1:1: $.replcas~: unknown field "replcas"`,
		"app.yaml:2:10: $.timeout: expected integer, got string",
		"app.yaml:4:11: $.servers[0].port: expected integer, got string",
	))
	require.ErrorIs(t, err, niceyaml.ErrDecode)
	requireInvalid(t, err, 3)

	assert.Equal(t, stringtest.JoinLF(
		"app.yaml: 3 problems",
		`|-- 1:1: $.replcas~: unknown field "replcas"`,
		"|-- 2:10: $.timeout: expected integer, got string",
		"`-- 4:11: $.servers[0].port: expected integer, got string",
		"",
		"   1 | replcas: 3",
		`     | ^^^^^^^ unknown field "replcas"`,
		"   2 | timeout: soon",
		"     |          ^^^^ expected integer, got string",
		"     | ...",
		"   4 |   - port: http",
		"     |           ^^^^ expected integer, got string",
	), strings.TrimRight(niceyaml.FormatError(err, 0), "\n"))

	var srcErr *niceyaml.SourceError

	require.ErrorAs(t, err, &srcErr)

	// The summary counts the problems and points at none.
	_, ok := srcErr.Position()
	assert.False(t, ok)

	_, ok = srcErr.Path()
	assert.False(t, ok)

	problems := srcErr.Errors()
	require.Len(t, problems, 3)

	want := []struct {
		path string
		pos  position.Position
	}{
		{path: "$.replcas~", pos: position.New(0, 0)},
		{path: "$.timeout", pos: position.New(1, 9)},
		{path: "$.servers[0].port", pos: position.New(3, 10)},
	}

	for i, problem := range problems {
		require.ErrorIs(t, problem, niceyaml.ErrDecode)
		require.NoError(t, problem.Unresolved())

		path, ok := problem.Path()
		require.True(t, ok)
		assert.Equal(t, want[i].path, path.String())

		pos, ok := problem.Position()
		require.True(t, ok)
		assert.Equal(t, want[i].pos, pos)
	}

	// Each problem holds the rejection the decoder returned for it.
	var unknown *yaml.UnknownFieldError

	require.ErrorAs(t, problems[0], &unknown)
	assert.Equal(t, "replcas", unknown.Token.Value)

	for i, value := range []string{"soon", "http"} {
		var mismatch *yaml.TypeError

		require.ErrorAs(t, problems[i+1], &mismatch)
		assert.Equal(t, value, mismatch.Token.Value)
	}
}

func TestDocument_Decode_Problems(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error
		input  string
		scope  string
		want   string
		opts   []niceyaml.DecodeOption
	}{
		// One problem comes back as the error itself, with no summary.
		"one problem": {
			decode: decodeInto[problemConfig](),
			input:  "timeout: soon\nreplicas: 3\n",
			want:   "1:10: $.timeout: expected integer, got string",
		},
		"one unknown field": {
			decode: decodeInto[problemConfig](),
			input:  "tmeout: 1\n",
			opts:   []niceyaml.DecodeOption{strict},
			want:   `1:1: $.tmeout~: unknown field "tmeout"`,
		},
		// The rows come in the order of the source, whatever order the
		// target declares its fields in.
		"scalars of the wrong kind": {
			decode: decodeInto[problemConfig](),
			input:  "timeout: soon\nreplicas: 1.5x\nservers:\n  - port: http\n  - name: [a]\nlabels: {a: x, b: 2, c: y}\n",
			want: stringtest.JoinLF(
				"6 problems",
				"1:10: $.timeout: expected integer, got string",
				"2:11: $.replicas: expected integer, got string",
				"4:11: $.servers[0].port: expected integer, got string",
				"5:12: $.servers[1].name: expected string, got sequence",
				"6:13: $.labels.a: expected integer, got string",
				"6:25: $.labels.c: expected integer, got string",
			),
		},
		// A mapping or a sequence where the target takes another kind is
		// one problem, and the walk reads nothing below it.
		"mappings and sequences of the wrong kind": {
			decode: decodeInto[problemConfig](),
			input:  "timeout: {a: soon}\nreplicas: [x, y]\nservers:\n  - a\n  - [b]\nlabels: [c]\n",
			want: stringtest.JoinLF(
				"5 problems",
				"1:11: $.timeout: expected integer, got mapping",
				"2:12: $.replicas: expected integer, got sequence",
				"4:5: $.servers[0]: expected mapping, got string",
				"5:6: $.servers[1]: expected mapping, got sequence",
				"6:10: $.labels: expected mapping, got sequence",
			),
		},
		"numbers out of range": {
			decode: decodeInto[struct {
				Small int8   `yaml:"small"`
				Wide  uint16 `yaml:"wide"`
				Count uint   `yaml:"count"`
			}](),
			input: "small: 300\nwide: 70000\ncount: -1\n",
			want: stringtest.JoinLF(
				"3 problems",
				"1:8: $.small: expected integer from -128 to 127, got 300",
				"2:7: $.wide: expected integer from 0 to 65535, got 70000",
				"3:8: $.count: expected integer from 0 to 18446744073709551615, got -1",
			),
		},
		// The decoder parses a duration itself and reports the error of
		// the parse with no token.
		"durations": {
			decode: decodeInto[problemConfig](),
			input: "servers:\n  - {name: a, timeout: soon}\n  - {name: b, timeout: \"5\"}\n" +
				"  - {name: c, timeout: 5}\n  - {name: d, timeout: [1]}\nreplicas: x\n",
			want: stringtest.JoinLF(
				"5 problems",
				`2:24: $.servers[0].timeout: time: invalid duration "soon"`,
				`3:24: $.servers[1].timeout: time: missing unit in duration "5"`,
				"4:24: $.servers[2].timeout: expected duration, got integer",
				"5:25: $.servers[3].timeout: expected duration, got sequence",
				"6:11: $.replicas: expected integer, got string",
			),
		},
		// The decoder reads text that is no timestamp as the zero time,
		// and rejects a value of another kind.
		"timestamps": {
			decode: decodeInto[struct {
				When  time.Time `yaml:"when"`
				Until time.Time `yaml:"until"`
				N     int       `yaml:"n"`
			}](),
			input: "when: later\nuntil: 5\nn: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"2:8: $.until: expected timestamp, got integer",
				"3:4: $.n: expected integer, got string",
			),
		},
		"unknown fields beside values": {
			decode: decodeInto[problemConfig](),
			input:  "timeout: soon\nservers:\n  - {name: a, timeout: soon, prt: 1}\nlabels: {a: x}\nreplcas: 2\n",
			opts:   []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				"5 problems",
				"1:10: $.timeout: expected integer, got string",
				`3:24: $.servers[0].timeout: time: invalid duration "soon"`,
				`3:30: $.servers[0].prt~: unknown field "prt"`,
				"4:13: $.labels.a: expected integer, got string",
				`5:1: $.replcas~: unknown field "replcas"`,
			),
		},
		// The decoder reads nothing from a null, so a null is no problem
		// for any type.
		"nulls": {
			decode: decodeInto[problemConfig](),
			input:  "timeout: soon\nreplicas: ~\nservers:\n  - ~\n  - {port: ~, timeout: null}\nlabels: {a: ~}\n",
			want:   "1:10: $.timeout: expected integer, got string",
		},
		"pointers and arrays": {
			decode: decodeInto[struct {
				P   *int           `yaml:"p"`
				PS  *problemServer `yaml:"ps"`
				PP  **int          `yaml:"pp"`
				Arr [2]int         `yaml:"arr"`
			}](),
			input: "p: x\nps: {port: y}\npp: z\narr: [1, w]\n",
			want: stringtest.JoinLF(
				"4 problems",
				"1:4: $.p: expected integer, got string",
				"2:12: $.ps.port: expected integer, got string",
				"3:5: $.pp: expected integer, got string",
				"4:10: $.arr[1]: expected integer, got string",
			),
		},
		// The decoder fills an interface with any value, so no value
		// below one is a problem.
		"values below an interface": {
			decode: decodeInto[struct {
				A any            `yaml:"a"`
				M map[string]any `yaml:"m"`
				L []any          `yaml:"l"`
				N int            `yaml:"n"`
			}](),
			input: "a: {port: [http]}\nm: {k: {port: http}}\nl: [x, {y: z}]\nn: w\n",
			want:  "4:4: $.n: expected integer, got string",
		},
		// The fields of an inline struct read the mapping of the struct
		// that holds it.
		"inline struct": {
			decode: decodeInto[struct {
				Name         string `yaml:"name"`
				StrictNested `yaml:",inline"`
			}](),
			input: "c: x\nd: y\nname: [n]\n",
			want: stringtest.JoinLF(
				"3 problems",
				"1:4: $.c: expected integer, got string",
				"2:4: $.d: expected integer, got string",
				"3:8: $.name: expected string, got sequence",
			),
		},
		// The decoder hands an inline map every entry of the mapping, in
		// no fixed order, so it rejects x in one decode and y in another.
		// The report holds both in every decode.
		"inline map": {
			decode: decodeInto[struct {
				Rest map[string]int `yaml:",inline"`
				A    bool           `yaml:"a"`
				B    bool           `yaml:"b"`
			}](),
			input: "x: bad\na: 1\ny: worse\nb: 2\n",
			want: stringtest.JoinLF(
				"4 problems",
				"1:4: $.x: expected integer, got string",
				"2:4: $.a: expected boolean, got integer",
				"3:4: $.y: expected integer, got string",
				"4:4: $.b: expected boolean, got integer",
			),
		},
		// The decoder names a value by the place its anchor defines it,
		// so a value several aliases read is one problem.
		"value several aliases read": {
			decode: decodeInto[struct {
				A int `yaml:"a"`
				B int `yaml:"b"`
				C int `yaml:"c"`
				D int `yaml:"d"`
			}](),
			input: "a: &x soon\nb: *x\nc: *x\nd: bad\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:7: $.a: expected integer, got string",
				"4:4: $.d: expected integer, got string",
			),
		},
		// The error of a parse has no token, so it takes the path of the
		// value that reads it, and each alias reads the duration anew.
		"duration several aliases read": {
			decode: decodeInto[struct {
				A any           `yaml:"a"`
				B time.Duration `yaml:"b"`
				C time.Duration `yaml:"c"`
				D int           `yaml:"d"`
			}](),
			input: "a: &x soon\nb: *x\nc: *x\nd: bad\n",
			want: stringtest.JoinLF(
				"3 problems",
				`2:4: $.b: time: invalid duration "soon"`,
				`3:4: $.c: time: invalid duration "soon"`,
				"4:4: $.d: expected integer, got string",
			),
		},
		// Two servers read the timeout of one anchor through a merge
		// key, which is one value and one problem.
		"values a merge key brings in": {
			decode: decodeInto[problemConfig](),
			input: "base: &b {port: http, timeout: soon}\nservers:\n  - *b\n  - {<<: *b, name: [a]}\n" +
				"labels: {<<: {k: x}, j: y}\nreplicas: z\n",
			want: stringtest.JoinLF(
				"6 problems",
				"1:17: $.base.port: expected integer, got string",
				`1:32: $.servers[0].timeout: time: invalid duration "soon"`,
				"4:21: $.servers[1].name: expected string, got sequence",
				"5:18: $.labels.<<.k: expected integer, got string",
				"5:25: $.labels.j: expected integer, got string",
				"6:11: $.replicas: expected integer, got string",
			),
		},
		// The decoder leaves out what the merge key brings in for a
		// struct that ignores merge keys, so b is no problem.
		"struct that ignores merge keys": {
			decode: decodeInto[struct {
				Defs *StrictBase   `yaml:"defs"`
				V    strictAliased `yaml:"v"`
			}](),
			input: "v:\n  <<: {b: bad, in: {a: worse}}\n  in: {b: x}\n",
			want:  "3:11: $.v.in.b: expected integer, got string",
		},
		// The decoder refuses a sequence of sources for a struct and
		// decodes none of its fields, so b is no problem beside the
		// refusal.
		"struct whose merge key the decoder refuses": {
			decode: decodeInto[struct {
				V strictInner `yaml:"v"`
				N int         `yaml:"n"`
			}](),
			input: "v:\n  <<: [{a: x}]\n  b: bad\nn: z\n",
			want: stringtest.JoinLF(
				"2 problems",
				"2:9: $.v.<<: expected mapping, got sequence",
				"4:4: $.n: expected integer, got string",
			),
		},
		// The decoder decodes no field from a mapping that holds a key it
		// does not read as a string, and takes no entry from one that a
		// merge key brings in.
		"mappings with a key that is no string": {
			decode: decodeInto[struct {
				V strictInner `yaml:"v"`
				W strictInner `yaml:"w"`
				N int         `yaml:"n"`
			}](),
			input: "v:\n  1: q\n  a: bad\nw:\n  <<: {true: q, a: bad}\n  b: worse\nn: z\n",
			want: stringtest.JoinLF(
				"2 problems",
				"6:6: $.w.b: expected integer, got string",
				"7:4: $.n: expected integer, got string",
			),
		},
		// The decoder holds the value of an anchor and assigns it to the
		// target of an alias that takes it, so the alias passes where
		// the value alone does not decode.
		"alias to a value the decoder assigns": {
			decode: decodeInto[struct {
				Defs any   `yaml:"defs"`
				L    []int `yaml:"l"`
				X    int   `yaml:"x"`
				N    int   `yaml:"n"`
			}](),
			input: "defs: {a: &a !!int 1.5}\nl: [*a, 2]\nx: *a\nn: z\n",
			want:  "4:4: $.n: expected integer, got string",
		},
		// The decoder rejects a tag that does not convert its value
		// before it decodes any value, and the others still report.
		"tag that does not convert its value": {
			decode: decodeInto[problemConfig](),
			input:  "timeout: !!bool maybe\nreplicas: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				`1:17: $.timeout: cannot convert "maybe" to boolean`,
				"2:11: $.replicas: expected integer, got string",
			),
		},
		// The paths read from the root of the document.
		"scoped decode": {
			decode: decodeInto[problemServer](),
			input:  "base: &b {port: http}\nservers:\n  - {name: a, port: 1}\n  - {<<: *b, name: [b], timeout: soon}\n",
			scope:  "$.servers[1]",
			want: stringtest.JoinLF(
				"3 problems",
				"1:17: $.base.port: expected integer, got string",
				"4:21: $.servers[1].name: expected string, got sequence",
				`4:34: $.servers[1].timeout: time: invalid duration "soon"`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The decoder picks the unknown field and the inline map value
			// it reports in no fixed order, so several decodes show that
			// the report does not depend on its pick. Every second decode
			// reads a source with a reference document. The decoder then
			// reads each anchor of the document under a new name, and the
			// report stays the same.
			for i := range 20 {
				var srcOpts []niceyaml.SourceOption

				if i%2 == 1 {
					srcOpts = append(srcOpts, niceyaml.WithReferences(
						niceyaml.NewSourceFromString("a: &a 2\nb: &b {port: 1}\nx: &x 1\n"),
					))
				}

				node := yamltest.FirstDocument(t, tc.input, srcOpts...)
				if tc.scope != "" {
					node = yamltest.At(t, node, paths.MustParse(tc.scope))
				}

				err := tc.decode(t.Context(), node, tc.opts...)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)
				assert.Same(t, node, srcErr.Node())

				rows := strings.Count(tc.want, "\n")

				requireInvalid(t, err, max(rows, 1))

				// One problem points at its value, and a summary at none.
				_, ok := srcErr.Position()
				assert.Equal(t, rows == 0, ok)

				_, ok = srcErr.Path()
				assert.Equal(t, rows == 0, ok)

				problems := srcErr.Errors()
				require.Len(t, problems, rows)

				for _, problem := range problems {
					require.ErrorIs(t, problem, niceyaml.ErrDecode)
					require.NoError(t, problem.Unresolved())

					_, ok := problem.Position()
					assert.True(t, ok)

					_, ok = problem.Path()
					assert.True(t, ok)
				}
			}
		})
	}
}

func TestDocument_Decode_Problems_SecondDocument(t *testing.T) {
	t.Parallel()

	// A problem of a later document keeps its line in the file.
	docs, err := niceyaml.NewSourceFromString(
		"replicas: 1\n---\nreplicas: x\ntimeout: soon\n",
		niceyaml.WithName("app.yaml"),
	).Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	var cfg problemConfig

	err = docs[1].DecodeInto(t.Context(), &cfg)
	require.EqualError(t, err, stringtest.JoinLF(
		"app.yaml: document 2: 2 problems",
		"app.yaml:3:11: $.replicas: expected integer, got string",
		"app.yaml:4:10: $.timeout: expected integer, got string",
	))
}

func TestDocument_Decode_Problems_Unmarshalers(t *testing.T) {
	t.Parallel()

	custom := niceyaml.WithYAMLDecodeOptions(yaml.CustomUnmarshaler(decodePulled))

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error
		is     error
		input  string
		want   string
		opts   []niceyaml.DecodeOption
	}{
		// The unmarshaler reads pull as text, so the boolean field of
		// the type is no problem, and its own error stays the report.
		"unmarshaler whose fields do not mirror the document": {
			decode: decodeInto[struct {
				Image    problemImage `yaml:"image"`
				Replicas int          `yaml:"replicas"`
			}](),
			input: "image: {pull: always}\n",
			is:    errRepoRequired,
			want:  "1:9: $.image: repo is required",
		},
		"unmarshaler error beside a value": {
			decode: decodeInto[struct {
				Image    problemImage `yaml:"image"`
				Replicas int          `yaml:"replicas"`
			}](),
			input: "image: {pull: always}\nreplicas: x\n",
			is:    errRepoRequired,
			want: stringtest.JoinLF(
				"2 problems",
				"1:9: $.image: repo is required",
				"2:11: $.replicas: expected integer, got string",
			),
		},
		"value beside an unmarshaler that passes": {
			decode: decodeInto[struct {
				Image    problemImage `yaml:"image"`
				Replicas int          `yaml:"replicas"`
			}](),
			input: "replicas: x\nimage: {repo: r, pull: always}\n",
			want:  "1:11: $.replicas: expected integer, got string",
		},
		// Nothing shows which type the option decodes, so the error of
		// its function has no location and comes back alone.
		"custom unmarshaler function": {
			decode: decodeInto[struct {
				Image    problemPulled `yaml:"image"`
				Replicas int           `yaml:"replicas"`
			}](),
			input: "image: {pull: always}\nreplicas: x\n",
			opts:  []niceyaml.DecodeOption{custom},
			is:    errRepoRequired,
			want:  "repo is required",
		},
		// The struct the function decodes confirms each of its fields,
		// so the function, which takes pull as text, has the last word.
		"value beside a custom unmarshaler function that passes": {
			decode: decodeInto[struct {
				Image    problemPulled `yaml:"image"`
				Replicas int           `yaml:"replicas"`
			}](),
			input: "replicas: x\nimage: {repo: r, pull: always}\n",
			opts:  []niceyaml.DecodeOption{custom},
			want:  "1:11: $.replicas: expected integer, got string",
		},
		// The error of the unmarshaler keeps its wrapper and its
		// sentinel beside the other problems.
		"unmarshaler that wraps a sentinel": {
			decode: decodeInto[struct {
				Server   problemWrapped `yaml:"server"`
				Replicas int            `yaml:"replicas"`
				Limit    int            `yaml:"limit"`
			}](),
			input: "server: {timeout: soon}\nreplicas: x\nlimit: y\n",
			is:    errBadServer,
			want: stringtest.JoinLF(
				"3 problems",
				`1:10: $.server: bad server: time: invalid duration "soon"`,
				"2:11: $.replicas: expected integer, got string",
				"3:8: $.limit: expected integer, got string",
			),
		},
		// An enum reports one value for each decode, as its unmarshaler
		// alone says what it takes.
		"values that decode themselves": {
			decode: decodeInto[struct {
				Tiers []tier `yaml:"tiers"`
				N     int    `yaml:"n"`
			}](),
			input: "tiers: [low, mid, top]\nn: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:14: $.tiers[1]: unknown tier "mid"`,
				"2:4: $.n: expected integer, got string",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.decode(t.Context(), yamltest.FirstDocument(t, tc.input), tc.opts...)
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)
			requireInvalid(t, err, max(strings.Count(tc.want, "\n"), 1))

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}
		})
	}
}

func TestDocument_Decode_Problems_References(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The reference document defines b, as some of the documents do.
	references := "defaults: &defaults {port: http}\nlimit: &limit soon\nb: &b {port: 1}\n"

	// The decoder reads the timeout before any other field.
	type timeoutFirst struct {
		Timeout  int             `yaml:"timeout"`
		Replicas int             `yaml:"replicas"`
		Servers  []problemServer `yaml:"servers"`
	}

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error
		input  string
		scope  string
		want   string
		opts   []niceyaml.DecodeOption
	}{
		// An alias reads the anchor of its own document, so the port of
		// the reference document is no part of the report.
		"anchor name the reference document defines too": {
			decode: decodeInto[problemConfig](),
			input:  "base: &b {port: bad}\nservers:\n  - *b\n  - {<<: *b, name: [a]}\nreplicas: x\n",
			want: stringtest.JoinLF(
				"3 problems",
				"1:17: $.base.port: expected integer, got string",
				"4:21: $.servers[1].name: expected string, got sequence",
				"5:11: $.replicas: expected integer, got string",
			),
		},
		// The document holds no line for a value of a reference document,
		// so the search reads nothing behind an alias to one. The port the
		// merge key brings in reports in a later decode.
		"mapping that merges a reference document": {
			decode: decodeInto[problemConfig](),
			input:  "servers:\n  - {<<: *defaults, name: [a], prt: 1}\n  - {port: y, nme: 2}\nreplicas: x\nrplicas: 2\n",
			opts:   []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				"6 problems",
				"2:28: $.servers[0].name: expected string, got sequence",
				`2:32: $.servers[0].prt~: unknown field "prt"`,
				"3:12: $.servers[1].port: expected integer, got string",
				`3:15: $.servers[1].nme~: unknown field "nme"`,
				"4:11: $.replicas: expected integer, got string",
				`5:1: $.rplicas~: unknown field "rplicas"`,
			),
		},
		// The decoder rejects the timeout of the reference document first.
		// That rejection binds at the alias, and the others report beside
		// it.
		"rejection inside a reference document": {
			decode: decodeInto[timeoutFirst](),
			input:  "replicas: x\ntimeout: *limit\nservers:\n  - {port: y}\n",
			want: stringtest.JoinLF(
				"3 problems",
				"1:11: $.replicas: expected integer, got string",
				"2:10: expected integer, got string",
				"4:12: $.servers[0].port: expected integer, got string",
			),
		},
		// The decoder rejects the port first, and the search reads nothing
		// behind the alias, so the timeout reports in a later decode.
		"value of a reference document beside a rejection": {
			decode: decodeInto[problemConfig](),
			input:  "replicas: x\ntimeout: *limit\nservers:\n  - {port: y}\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:11: $.replicas: expected integer, got string",
				"4:12: $.servers[0].port: expected integer, got string",
			),
		},
		// Two aliases read the reference document, and nothing says which
		// of them led to the rejection. It binds at no position, so it
		// comes back alone.
		"rejection with no position": {
			decode: decodeInto[problemConfig](),
			input:  "replicas: x\ntimeout: *limit\nservers:\n  - *defaults\n  - {port: y}\n",
			want:   "expected integer, got string",
		},
		"scoped decode": {
			decode: decodeInto[problemServer](),
			input:  "base: &b {port: bad}\nservers:\n  - {name: a}\n  - {<<: *b, name: [b], timeout: *limit}\n",
			scope:  "$.servers[1]",
			want: stringtest.JoinLF(
				"2 problems",
				"1:17: $.base.port: expected integer, got string",
				"4:21: $.servers[1].name: expected string, got sequence",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for range 10 {
				node := yamltest.FirstDocument(t, tc.input,
					niceyaml.WithReferences(niceyaml.NewSourceFromString(references)))
				if tc.scope != "" {
					node = yamltest.At(t, node, paths.MustParse(tc.scope))
				}

				err := tc.decode(t.Context(), node, tc.opts...)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				requireInvalid(t, err, max(strings.Count(tc.want, "\n"), 1))
			}
		})
	}
}

func TestDocument_DecodeInto_Problems_Defaults(t *testing.T) {
	t.Parallel()

	input := "timeout: soon\nserver: {name: a}\n"

	// The decode fills the server over the port the caller set, so the
	// check for a port passes. No second decode of the server into a new
	// value runs that check without the port.
	t.Run("unmarshaler", func(t *testing.T) {
		t.Parallel()

		cfg := struct {
			Server  problemPorted `yaml:"server"`
			Timeout int           `yaml:"timeout"`
		}{Server: problemPorted{Port: 8080}}

		err := yamltest.FirstDocument(t, input).DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "1:10: $.timeout: expected integer, got string")
		require.NotErrorIs(t, err, errPortRequired)
	})

	t.Run("struct validator", func(t *testing.T) {
		t.Parallel()

		cfg := struct {
			Server  problemServer `yaml:"server"`
			Timeout int           `yaml:"timeout"`
		}{Server: problemServer{Port: 8080}}

		err := yamltest.FirstDocument(t, input).DecodeInto(
			t.Context(), &cfg, niceyaml.WithYAMLDecodeOptions(yaml.Validator(portValidator{})),
		)
		require.EqualError(t, err, "1:10: $.timeout: expected integer, got string")
		require.NotErrorIs(t, err, errPortRequired)
	})
}

func TestDocument_Decode_Problems_UnmarshalerCalls(t *testing.T) {
	t.Parallel()

	// The unmarshaler takes each name once. A second call for a name it
	// has seen would report a duplicate.
	t.Run("unmarshaler with state", func(t *testing.T) {
		t.Parallel()

		calls := &problemCalls{seen: map[string]bool{}}
		ctx := context.WithValue(t.Context(), callsKey{}, calls)

		_, err := yamltest.FirstDocument(t, "names: [a, b, c]\ntimeout: soon\nreplicas: x\n").Decode[struct {
			Names    []problemUnique `yaml:"names"`
			Timeout  int             `yaml:"timeout"`
			Replicas int             `yaml:"replicas"`
		}](ctx)
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"2:10: $.timeout: expected integer, got string",
			"3:11: $.replicas: expected integer, got string",
		))
		assert.Len(t, calls.seen, 3)
	})

	// A decode of the struct would call the unmarshaler of its inline
	// field, so nothing confirms the fields beside it, and the decoder
	// reports them one for each decode.
	t.Run("inline field that decodes itself", func(t *testing.T) {
		t.Parallel()

		calls := &problemCalls{}
		ctx := context.WithValue(t.Context(), callsKey{}, calls)

		_, err := yamltest.FirstDocument(t, "name: a\nport: http\nlimit: x\nrest: {port: y}\n").Decode[struct {
			Meta  problemCounted `yaml:",inline"`
			Port  int            `yaml:"port"`
			Rest  problemServer  `yaml:"rest"`
			Limit int            `yaml:"limit"`
		}](ctx)
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"2:7: $.port: expected integer, got string",
			"4:14: $.rest.port: expected integer, got string",
		))
		assert.Equal(t, 1, calls.count)
	})
}

func TestDocument_Decode_Problems_Context(t *testing.T) {
	t.Parallel()

	type config struct {
		Ending   problemEnding `yaml:"ending"`
		Replicas int           `yaml:"replicas"`
		Timeout  int           `yaml:"timeout"`
	}

	input := "ending: now\nreplicas: x\ntimeout: soon\n"

	t.Run("context that goes on", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, input).Decode[config](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"2:11: $.replicas: expected integer, got string",
			"3:10: $.timeout: expected integer, got string",
		))
	})

	// The decoder never checks the context, so the decode still returns
	// its rejection, and the search for the others does not run.
	t.Run("context that ends in the decode", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		ctx = context.WithValue(ctx, callsKey{}, &problemCalls{cancel: cancel})

		_, err := yamltest.FirstDocument(t, input).Decode[config](ctx)
		require.EqualError(t, err, "2:11: $.replicas: expected integer, got string")
	})

	// The function decodes the image in the decode, and ends the context
	// when the search asks it about a field of the image. The search has
	// found the timeout by then, and reports none of what it found.
	t.Run("context that ends in the search", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		calls := 0
		ending := yaml.CustomUnmarshaler(func(*problemPulled, []byte) error {
			calls++
			if calls > 1 {
				cancel()
			}

			return nil
		})

		_, err := yamltest.FirstDocument(t, "replicas: x\ntimeout: soon\nimage: {pull: always}\n").Decode[struct {
			Replicas int           `yaml:"replicas"`
			Timeout  int           `yaml:"timeout"`
			Image    problemPulled `yaml:"image"`
		}](ctx, niceyaml.WithYAMLDecodeOptions(ending))
		require.EqualError(t, err, "1:11: $.replicas: expected integer, got string")
		assert.Equal(t, 2, calls)
	})
}

func TestDocument_Decode_Problems_BoundElsewhere(t *testing.T) {
	t.Parallel()

	// The unmarshaler returns an error bound to a source of its own. Its
	// position lies in that source, so nothing joins it.
	_, err := yamltest.FirstDocument(t, "nested: {prt: 80}\nreplicas: x\n").Decode[struct {
		Nested   problemNested `yaml:"nested"`
		Replicas int           `yaml:"replicas"`
	}](t.Context())
	require.EqualError(t, err, `inner.yaml:1:2: $.prt~: unknown field "prt"`)
}

func TestDocument_Decode_Problems_Deep(t *testing.T) {
	t.Parallel()

	// The search reads each level once, so a document nested this deep
	// takes no longer for it.
	const depth = 4000

	_, err := yamltest.FirstDocument(t, deepDocument(depth)).Decode[problemDeep](t.Context())
	require.ErrorIs(t, err, niceyaml.ErrDecode)

	var srcErr *niceyaml.SourceError

	require.ErrorAs(t, err, &srcErr)
	assert.Empty(t, srcErr.Errors())
	assert.Equal(t, "expected integer, got string", srcErr.Message())

	path, ok := srcErr.Path()
	require.True(t, ok)
	assert.Equal(t, "$"+strings.Repeat(".c", depth)+".v", path.String())
}

func TestDocument_Decode_Problems_Many(t *testing.T) {
	t.Parallel()

	// The report holds every problem, and the message lists the first of
	// them and counts the rest.
	const servers = 500

	_, err := yamltest.FirstDocument(t, "servers:\n"+strings.Repeat("  - port: http\n", servers)).
		Decode[problemConfig](t.Context())
	require.ErrorIs(t, err, niceyaml.ErrDecode)

	var srcErr *niceyaml.SourceError

	require.ErrorAs(t, err, &srcErr)
	require.Len(t, srcErr.Errors(), servers)

	lines := strings.Split(err.Error(), "\n")
	require.Len(t, lines, niceyaml.ErrorListLimit+2)
	assert.Equal(t, "500 problems", lines[0])
	assert.Equal(t, "2:11: $.servers[0].port: expected integer, got string", lines[1])
	assert.Equal(t, "and 490 more", lines[len(lines)-1])
}
