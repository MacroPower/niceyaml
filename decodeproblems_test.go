package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

var (
	// The errors the unmarshalers of this file report.
	errBadServer    = errors.New("bad server")
	errRepoRequired = errors.New("repo is required")
	errPortRequired = errors.New("port is required")

	// Has go-yaml decode a problemRegistered as decodePulled decodes a
	// problemPulled, in every decode of the program.
	registerPulled = sync.OnceFunc(func() {
		yaml.RegisterCustomUnmarshalerContext(func(ctx context.Context, p *problemRegistered, data []byte) error {
			return decodePulled(ctx, (*problemPulled)(p), func(v any) error {
				err := yaml.Unmarshal(data, v)
				if err != nil {
					return fmt.Errorf("decode image: %w", err)
				}

				return nil
			})
		})
	})

	// The calls of the UnmarshalText method of [problemLevel], by text.
	levelCalls callCounter

	// The calls of the UnmarshalJSON method of [problemEither].
	eitherCalls callCounter

	// The calls of the UnmarshalText method of [problemOnce], by text.
	onceCalls callCounter
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

// problemTiered is a server whose tier decodes itself from text.
type problemTiered struct {
	Name string `yaml:"name"`
	Tier tier   `yaml:"tier"`
	Port int    `yaml:"port"`
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
// function from niceyaml.WithCustomUnmarshaler decodes it.
type problemPulled struct {
	Repo string `yaml:"repo"`
	Pull bool   `yaml:"pull"`
}

// problemRegistered has the fields of [problemPulled]. A function from
// yaml.RegisterCustomUnmarshaler decodes it in every decode of the
// program, as registerPulled registers one.
type problemRegistered problemPulled

// problemJSON has the fields of [problemPulled] and an UnmarshalJSON
// method that accepts every value, which the decoder calls only under
// niceyaml.WithJSONUnmarshalers.
type problemJSON problemPulled

func (*problemJSON) UnmarshalJSON([]byte) error { return nil }

// decodePulled decodes a [problemPulled] as [problemImage] decodes
// itself, through decode.
func decodePulled(_ context.Context, p *problemPulled, decode func(any) error) error {
	var raw struct {
		Repo string `yaml:"repo"`
		Pull string `yaml:"pull"`
	}

	err := decode(&raw)
	if err != nil {
		return err
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

// problemUniqueText rejects a name another value of the decode holds, as
// [problemUnique] does, and has an UnmarshalText method too. The decoder
// finds its UnmarshalYAML method first and never calls the other.
type problemUniqueText string

func (u *problemUniqueText) UnmarshalYAML(ctx context.Context, unmarshal func(any) error) error {
	return (*problemUnique)(u).UnmarshalYAML(ctx, unmarshal)
}

func (*problemUniqueText) UnmarshalText([]byte) error {
	return errBadServer
}

// uniqueTier decodes a [tier] in place of the UnmarshalText method of the
// type, as a function from niceyaml.WithCustomUnmarshaler does, and
// rejects a name another value of the decode holds.
func uniqueTier(ctx context.Context, _ *tier, decode func(any) error) error {
	calls, ok := ctx.Value(callsKey{}).(*problemCalls)
	if !ok {
		return nil
	}

	var name string

	err := decode(&name)
	if err != nil {
		return err
	}

	if calls.seen[name] {
		return fmt.Errorf("duplicate name %q", name)
	}

	calls.seen[name] = true

	return nil
}

// callCounter counts the calls of an unmarshaler that takes no context,
// under a name the unmarshaler gives each call. Every decode of the type
// shares the count, so one test alone decodes a type that counts in one.
type callCounter struct {
	calls map[string]int
	mu    sync.Mutex
}

// add counts one call under name and returns the calls counted under
// that name since the last take.
func (c *callCounter) add(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.calls == nil {
		c.calls = map[string]int{}
	}

	c.calls[name]++

	return c.calls[name]
}

// take returns the calls counted since the last take.
func (c *callCounter) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()

	calls := c.calls
	c.calls = nil

	return calls
}

// problemLevel decodes itself from text, takes "info" and "warn" alone,
// and counts its calls in levelCalls.
type problemLevel string

func (l *problemLevel) UnmarshalText(text []byte) error {
	level := string(text)

	levelCalls.add(level)

	if level != "info" && level != "warn" {
		return fmt.Errorf("unknown level %q", level)
	}

	*l = problemLevel(level)

	return nil
}

// problemOnce decodes itself from text, takes each text once, and panics
// when it gets a text again. It counts its calls in onceCalls.
type problemOnce string

func (o *problemOnce) UnmarshalText(text []byte) error {
	if onceCalls.add(string(text)) > 1 {
		panic("text decoded twice")
	}

	*o = problemOnce(text)

	return nil
}

// problemEither decodes itself from the text of a scalar. Under
// [niceyaml.WithJSONUnmarshalers], the decoder hands its UnmarshalJSON
// method a mapping or a sequence, and the method counts its calls in
// eitherCalls.
type problemEither struct{}

func (*problemEither) UnmarshalText([]byte) error {
	return nil
}

func (*problemEither) UnmarshalJSON([]byte) error {
	eitherCalls.add("json")

	return nil
}

// problemTextual has the methods of [problemEither] and counts no call,
// so several tests decode it at once.
type problemTextual struct{}

func (*problemTextual) UnmarshalText([]byte) error {
	return nil
}

func (*problemTextual) UnmarshalJSON([]byte) error {
	return nil
}

// problemPlaced decodes itself from text and reports an error that names
// a position already.
type problemPlaced string

func (*problemPlaced) UnmarshalText([]byte) error {
	return niceyaml.Invalid(errBadServer, niceyaml.AtPosition(position.New(0, 0)))
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

// problemLocated decodes itself from a word and returns the error the
// word names, located as an unmarshaler locates its own error. The
// decoder hands it the node of its value, and the token of that node
// gives the position of the word. It panics for the word "panic" and
// accepts a word it does not know.
type problemLocated struct{}

func (*problemLocated) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	tk := node.GetToken()
	at := position.NewFromToken(tk)
	port := paths.Doc().Child("port")

	switch tk.Value {
	case "plain":
		return errBadServer

	case "position":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPosition(at))

	case "range":
		end := position.New(at.Line, at.Col+len(tk.Value))

		return niceyaml.Invalid(errBadServer, niceyaml.AtRange(position.NewRange(at, end)))

	case "far":
		far := position.New(at.Line+100, at.Col)

		return niceyaml.Invalid(errBadServer, niceyaml.AtRange(position.NewRange(far, far)))

	case "path":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPath(port))

	case "absent":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPath(paths.Doc().Child("absent")))

	case "below":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPath(port.Child("unit")))

	case "unplaced":
		//nolint:wrapcheck // The test checks where a decode places the result as it is.
		return niceyaml.BindValue(niceyaml.Invalid(errBadServer, niceyaml.AtPath(port)))

	case "ambiguous":
		// The keys 1 and "1" share a path, and the rate under the first
		// has no limit.
		//nolint:wrapcheck // The test checks where a decode places the result as it is.
		return niceyaml.SelfValidateValue(ctx, map[any]layerRate{1: {}, "1": {Limit: 1}})

	case "elsewhere":
		inner := niceyaml.NewSourceFromString("port: 1\n", niceyaml.WithName("inner.yaml"))

		return inner.Bind(niceyaml.Invalid(errBadServer, niceyaml.AtPath(port)))

	case "panic":
		panic(errBadServer)

	default:
		return nil
	}
}

// problemTimed is the target of a document whose first rejection names no
// token. The decoder reads the fields in the order below. It thus rejects
// a value that reports its own error, a timeout, or a tier ahead of the
// port, and it rejects the port at a token.
type problemTimed struct {
	Own     problemLocated `yaml:"own"`
	Timeout time.Duration  `yaml:"timeout"`
	Tier    tier           `yaml:"tier"`
	Port    uint16         `yaml:"port"`
}

// decodeRows returns what a report reads from err, the error of a decode
// of doc, as one row for each node of its [niceyaml.ErrorTree]. A row
// holds the name of the source, the text and the message of the node,
// its path, its position, and its range. It also says whether the node
// has a path and a position, is bound to doc, matches
// [niceyaml.ErrDecode], and blames the document.
func decodeRows(doc *niceyaml.Node, err error) []string {
	var rows []string

	for node := range niceyaml.NewErrorTree(err).All() {
		path, pathed := node.Path()
		pos, located := node.Bound.Position()
		rng, _ := node.Bound.Range()

		rows = append(rows, fmt.Sprintf(
			"%s | %s | %s | %s %t | %s %s %t | bound=%t decode=%t invalid=%t",
			node.Bound.Source().Name(), node.Text, node.Message(), path, pathed, pos, rng, located,
			node.Bound.Node() == doc, errors.Is(node.Err, niceyaml.ErrDecode), node.IsInvalid(),
		))
	}

	return rows
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
		require.True(t, problem.IsInvalid(), problem.Text)

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
	), strings.TrimRight(niceyaml.FormatError(err, niceyaml.WithContextLines(0)), "\n"))

	var srcErr *niceyaml.SourceError

	require.ErrorAs(t, err, &srcErr)

	// The summary counts the problems and points at none.
	_, ok := srcErr.Position()
	assert.False(t, ok)

	_, ok = srcErr.Path()
	assert.False(t, ok)

	problems := srcErr.Members()
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
				"5:5: $.servers[1].name: expected string, got sequence",
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
				"1:1: $.timeout: expected integer, got mapping",
				"2:1: $.replicas: expected integer, got sequence",
				"4:5: $.servers[0]: expected mapping, got string",
				"5:3: $.servers[1]: expected mapping, got sequence",
				"6:1: $.labels: expected mapping, got sequence",
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
				"5:15: $.servers[3].timeout: expected duration, got sequence",
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
				"3:1: $.name: expected string, got sequence",
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
				"4:14: $.servers[1].name: expected string, got sequence",
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
				"2:3: $.v.<<: expected mapping, got sequence",
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
				"4:14: $.servers[1].name: expected string, got sequence",
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

				problems := srcErr.Members()
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
	docs := niceyaml.NewSourceFromString(
		"replicas: 1\n---\nreplicas: x\ntimeout: soon\n",
		niceyaml.WithName("app.yaml"),
	).Documents()
	require.Len(t, docs, 2)

	var cfg problemConfig

	err := docs[1].DecodeInto(t.Context(), &cfg)
	require.EqualError(t, err, stringtest.JoinLF(
		"app.yaml: document 2: 2 problems",
		"app.yaml:3:11: $.replicas: expected integer, got string",
		"app.yaml:4:10: $.timeout: expected integer, got string",
	))
}

func TestDocument_Decode_Problems_Unmarshalers(t *testing.T) {
	t.Parallel()

	custom := niceyaml.WithCustomUnmarshaler(decodePulled)

	registerPulled()

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
			want:  "1:1: $.image: repo is required",
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
				"1:1: $.image: repo is required",
				"2:11: $.replicas: expected integer, got string",
			),
		},
		// The unmarshaler names a field of its value, and the report
		// holds the error at that field beside the other problem.
		"unmarshaler error at a path of its own beside a value": {
			decode: decodeInto[struct {
				Spans    []span `yaml:"spans"`
				Replicas int    `yaml:"replicas"`
			}](),
			input: "spans:\n  - {from: 9, to: 3}\nreplicas: x\n",
			is:    errSpan,
			want: stringtest.JoinLF(
				"2 problems",
				"2:19: $.spans[0].to: to is below from",
				"3:11: $.replicas: expected integer, got string",
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
		// The option names the type its function decodes, so the error
		// of the function binds at the value, beside the other problem.
		"custom unmarshaler function": {
			decode: decodeInto[struct {
				Image    problemPulled `yaml:"image"`
				Replicas int           `yaml:"replicas"`
			}](),
			input: "image: {pull: always}\nreplicas: x\n",
			opts:  []niceyaml.DecodeOption{custom},
			is:    errRepoRequired,
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.image: repo is required",
				"2:11: $.replicas: expected integer, got string",
			),
		},
		// The search leaves the image to the function, which takes pull
		// as text, so the boolean field of the type is no problem.
		"value beside a custom unmarshaler function that passes": {
			decode: decodeInto[struct {
				Image    problemPulled `yaml:"image"`
				Replicas int           `yaml:"replicas"`
			}](),
			input: "replicas: x\nimage: {repo: r, pull: always}\n",
			opts:  []niceyaml.DecodeOption{custom},
			want:  "1:11: $.replicas: expected integer, got string",
		},
		// Nothing shows which type a registered function decodes, so the
		// error of the function has no location and comes back alone.
		"registered unmarshaler function": {
			decode: decodeInto[struct {
				Image    problemRegistered `yaml:"image"`
				Replicas int               `yaml:"replicas"`
			}](),
			input: "image: {pull: always}\nreplicas: x\n",
			is:    errRepoRequired,
			want:  "repo is required",
		},
		// The struct the function decodes confirms each of its fields,
		// so the function, which takes pull as text, has the last word.
		"value beside a registered unmarshaler function that passes": {
			decode: decodeInto[struct {
				Image    problemRegistered `yaml:"image"`
				Replicas int               `yaml:"replicas"`
			}](),
			input: "replicas: x\nimage: {repo: r, pull: always}\n",
			want:  "1:11: $.replicas: expected integer, got string",
		},
		// The option turns the method on, so the search leaves the image
		// to the method, which accepts it.
		"json unmarshaler under its option": {
			decode: decodeInto[struct {
				Image    problemJSON `yaml:"image"`
				Replicas int         `yaml:"replicas"`
			}](),
			input: "replicas: x\nimage: {repo: [r], pull: always}\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			want:  "1:11: $.replicas: expected integer, got string",
		},
		// The decoder reads the fields of the type, so the search reads
		// them too.
		"json unmarshaler without its option": {
			decode: decodeInto[struct {
				Image    problemJSON `yaml:"image"`
				Replicas int         `yaml:"replicas"`
			}](),
			input: "replicas: x\nimage: {repo: [r], pull: always}\n",
			want: stringtest.JoinLF(
				"3 problems",
				"1:11: $.replicas: expected integer, got string",
				"2:9: $.image.repo: expected string, got sequence",
				"2:26: $.image.pull: expected boolean, got string",
			),
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
				`1:1: $.server: bad server: time: invalid duration "soon"`,
				"2:11: $.replicas: expected integer, got string",
				"3:8: $.limit: expected integer, got string",
			),
		},
		// An enum reports every value its UnmarshalText method rejects,
		// since the search hands the method each scalar again.
		"values that decode themselves": {
			decode: decodeInto[struct {
				Tiers []tier `yaml:"tiers"`
				N     int    `yaml:"n"`
			}](),
			input: "tiers: [low, mid, top]\nn: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"3 problems",
				`1:14: $.tiers[1]: unknown tier "mid"`,
				`1:19: $.tiers[2]: unknown tier "top"`,
				"2:4: $.n: expected integer, got string",
			),
		},
		// The decoder rejects the port first, and the search finds the
		// tier.
		"text value beside a value of the wrong kind": {
			decode: decodeInto[struct {
				Port int  `yaml:"port"`
				Tier tier `yaml:"tier"`
			}](),
			input: "tier: mid\nport: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:7: $.tier: unknown tier "mid"`,
				"2:7: $.port: expected integer, got string",
			),
		},
		// The decoder rejects the timeout first, the field its struct
		// declares before the tier.
		"text value beside a duration": {
			decode: decodeInto[tierServer](),
			input:  "tier: mid\ntimeout: soon\n",
			is:     errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:7: $.tier: unknown tier "mid"`,
				`2:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The map holds pointers, and the search reads the value each
		// points to.
		"text values of a map": {
			decode: decodeInto[struct {
				Tiers map[string]*tier `yaml:"tiers"`
			}](),
			input: "tiers: {a: mid, b: low, c: top}\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:12: $.tiers.a: unknown tier "mid"`,
				`1:28: $.tiers.c: unknown tier "top"`,
			),
		},
		// An alias reads the scalar into an element of its own, so the
		// element reports at the alias, as a duration behind one does.
		"alias to a text value": {
			decode: decodeInto[struct {
				Tiers []tier `yaml:"tiers"`
			}](),
			input: "tiers: [&t mid, *t, top]\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"3 problems",
				`1:12: $.tiers[0]: unknown tier "mid"`,
				`1:17: $.tiers[1]: unknown tier "mid"`,
				`1:21: $.tiers[2]: unknown tier "top"`,
			),
		},
		// The document writes the tier once, so both servers that merge
		// it report one problem.
		"text value a merge key brings in": {
			decode: decodeInto[struct {
				Servers []problemTiered `yaml:"servers"`
			}](),
			input: "base: &b {tier: mid}\nservers:\n  - {<<: *b, name: a}\n  - {<<: *b, name: b, port: x}\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:17: $.servers[0].tier: unknown tier "mid"`,
				"4:29: $.servers[1].port: expected integer, got string",
			),
		},
		// The search reads no key of a map, so the key reports in a
		// later decode.
		"text keys of a map": {
			decode: decodeInto[struct {
				N     int          `yaml:"n"`
				Tiers map[tier]int `yaml:"tiers"`
			}](),
			input: "n: x\ntiers: {mid: 1, low: 2}\n",
			want:  "1:4: $.n: expected integer, got string",
		},
		// The error of the method points at a place of its own, so the
		// search does not put it at the value.
		"text error that names a place": {
			decode: decodeInto[struct {
				N      int           `yaml:"n"`
				Placed problemPlaced `yaml:"placed"`
			}](),
			input: "n: x\nplaced: a\n",
			want:  "1:4: $.n: expected integer, got string",
		},
		// The decoder refuses a mapping or a sequence for a text type
		// with no call of its method, so the search reports each one.
		"mappings and sequences in a text type": {
			decode: decodeInto[struct {
				Tiers []tier `yaml:"tiers"`
				N     int    `yaml:"n"`
			}](),
			input: "tiers: [low, [a], mid, {b: 1}]\nn: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"4 problems",
				"1:14: $.tiers[1]: expected string, got sequence",
				`1:19: $.tiers[2]: unknown tier "mid"`,
				"1:24: $.tiers[3]: expected string, got mapping",
				"2:4: $.n: expected integer, got string",
			),
		},
		// The decoder rejects the port first, and the search finds the
		// sequence.
		"sequence in a text type beside a value of the wrong kind": {
			decode: decodeInto[struct {
				Port int  `yaml:"port"`
				Tier tier `yaml:"tier"`
			}](),
			input: "tier: [a]\nport: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.tier: expected string, got sequence",
				"2:7: $.port: expected integer, got string",
			),
		},
		// The decoder rejects the sequence first, and the search finds
		// it again, so the report holds it once.
		"sequence in a text type the decoder rejects first": {
			decode: decodeInto[struct {
				Tier tier `yaml:"tier"`
				Port int  `yaml:"port"`
			}](),
			input: "tier: [a]\nport: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.tier: expected string, got sequence",
				"2:7: $.port: expected integer, got string",
			),
		},
		// The option hands the mapping to the UnmarshalJSON method of the
		// type, so the search leaves it to that method.
		"mapping in a text type with an UnmarshalJSON method under its option": {
			decode: decodeInto[struct {
				Either map[string]problemTextual `yaml:"either"`
				Port   int                       `yaml:"port"`
			}](),
			input: "either: {a: {b: 1}}\nport: x\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			want:  "2:7: $.port: expected integer, got string",
		},
		"mapping in a text type with an UnmarshalJSON method": {
			decode: decodeInto[struct {
				Either map[string]problemTextual `yaml:"either"`
				Port   int                       `yaml:"port"`
			}](),
			input: "either: {a: {b: 1}}\nport: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:10: $.either.a: expected string, got mapping",
				"2:7: $.port: expected integer, got string",
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
				"4:14: $.servers[1].name: expected string, got sequence",
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
				"2:21: $.servers[0].name: expected string, got sequence",
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
				"4:14: $.servers[1].name: expected string, got sequence",
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
}

func TestDocument_Decode_Problems_UnmarshalerCalls(t *testing.T) {
	t.Parallel()

	// The unmarshaler takes each name once. A second call for a name it
	// has seen would report a duplicate.
	stateful := map[string]struct {
		decode func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error
		opts   []niceyaml.DecodeOption
	}{
		"unmarshaler with state": {
			decode: decodeInto[struct {
				Names    []problemUnique `yaml:"names"`
				Timeout  int             `yaml:"timeout"`
				Replicas int             `yaml:"replicas"`
			}](),
		},
		// The decoder calls the UnmarshalYAML method of the type and
		// never its UnmarshalText method, so the search hands the type no
		// scalar again.
		"unmarshaler with state and an UnmarshalText method": {
			decode: decodeInto[struct {
				Names    []problemUniqueText `yaml:"names"`
				Timeout  int                 `yaml:"timeout"`
				Replicas int                 `yaml:"replicas"`
			}](),
		},
		// The decoder calls the function in place of the UnmarshalText
		// method of the type, so the search hands the type no scalar
		// again.
		"custom unmarshaler function with state": {
			decode: decodeInto[struct {
				Names    []tier `yaml:"names"`
				Timeout  int    `yaml:"timeout"`
				Replicas int    `yaml:"replicas"`
			}](),
			opts: []niceyaml.DecodeOption{niceyaml.WithCustomUnmarshaler(uniqueTier)},
		},
	}

	for name, tc := range stateful {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := &problemCalls{seen: map[string]bool{}}
			ctx := context.WithValue(t.Context(), callsKey{}, calls)

			err := tc.decode(
				ctx,
				yamltest.FirstDocument(t, "names: [a, b, c]\ntimeout: soon\nreplicas: x\n"),
				tc.opts...,
			)
			require.EqualError(t, err, stringtest.JoinLF(
				"2 problems",
				"2:10: $.timeout: expected integer, got string",
				"3:11: $.replicas: expected integer, got string",
			))
			assert.Len(t, calls.seen, 3)
		})
	}

	// The decode calls the method once for each level. The second decode
	// that finds the value behind the rejection calls it again for the
	// levels up to "loud". The search calls it once more for each level,
	// and again to confirm each level the method rejects.
	t.Run("UnmarshalText method", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, stringtest.JoinLF(
			"services:",
			"  - {name: a, level: info, port: 1}",
			"  - {name: b, level: loud, port: x}",
			"  - {name: c, level: quiet, port: y}",
			"",
		)).Decode[struct {
			Services []struct {
				Name  string       `yaml:"name"`
				Level problemLevel `yaml:"level"`
				Port  int          `yaml:"port"`
			} `yaml:"services"`
		}](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"4 problems",
			`3:22: $.services[1].level: unknown level "loud"`,
			"3:34: $.services[1].port: expected integer, got string",
			`4:22: $.services[2].level: unknown level "quiet"`,
			"4:35: $.services[2].port: expected integer, got string",
		))
		requireInvalid(t, err, 4)
		assert.Equal(t, map[string]int{"info": 3, "loud": 4, "quiet": 3}, levelCalls.take())
	})

	// The decode calls the method once and rejects the port. The search
	// calls the method again, and the panic of that call adds no problem.
	t.Run("UnmarshalText method that panics in the search", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "port: x\nmode: fast\n").Decode[struct {
			Port int         `yaml:"port"`
			Mode problemOnce `yaml:"mode"`
		}](t.Context())
		require.EqualError(t, err, "1:7: $.port: expected integer, got string")
		requireInvalid(t, err, 1)
		assert.Equal(t, map[string]int{"fast": 2}, onceCalls.take())

		var p *niceyaml.PanicError

		assert.NotErrorAs(t, err, &p, "the error holds the panic of the search")
	})

	// The decoder hands the mapping to the UnmarshalJSON method of the
	// type. The search hands a type with an UnmarshalText method a scalar
	// alone, so it does not call the other method again.
	t.Run("UnmarshalJSON method beside an UnmarshalText method", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "either: {a: 1}\nport: x\n").Decode[struct {
			Either problemEither `yaml:"either"`
			Port   int           `yaml:"port"`
		}](t.Context(), niceyaml.WithJSONUnmarshalers(true))
		require.EqualError(t, err, "2:7: $.port: expected integer, got string")
		assert.Equal(t, map[string]int{"json": 1}, eitherCalls.take())
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

	// The registered function decodes the image in the decode, and ends
	// the context when the search asks it about a field of the image. The
	// search has found the timeout by then, and reports none of what it
	// found.
	t.Run("context that ends in the search", func(t *testing.T) {
		t.Parallel()

		type ended struct {
			Pull bool `yaml:"pull"`
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		calls := 0

		yaml.RegisterCustomUnmarshaler(func(*ended, []byte) error {
			calls++
			if calls > 1 {
				cancel()
			}

			return nil
		})

		_, err := yamltest.FirstDocument(t, "replicas: x\ntimeout: soon\nimage: {pull: always}\n").Decode[struct {
			Replicas int   `yaml:"replicas"`
			Timeout  int   `yaml:"timeout"`
			Image    ended `yaml:"image"`
		}](ctx)
		require.EqualError(t, err, "1:11: $.replicas: expected integer, got string")
		assert.Equal(t, 2, calls)
	})

	// The option names the type its function decodes, so the search asks
	// the function about no field of the image, and reports the timeout.
	t.Run("custom unmarshaler the search leaves alone", func(t *testing.T) {
		t.Parallel()

		calls := 0
		counting := niceyaml.WithCustomUnmarshaler(func(context.Context, *problemPulled, func(any) error) error {
			calls++

			return nil
		})

		_, err := yamltest.FirstDocument(t, "replicas: x\ntimeout: soon\nimage: {pull: always}\n").Decode[struct {
			Replicas int           `yaml:"replicas"`
			Timeout  int           `yaml:"timeout"`
			Image    problemPulled `yaml:"image"`
		}](t.Context(), counting)
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"1:11: $.replicas: expected integer, got string",
			"2:10: $.timeout: expected integer, got string",
		))
		assert.Equal(t, 1, calls)
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

func TestLayers_Decode_Problems(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The decoder names no token for the value it rejects first in each
	// input. The layers hold one file, so their decode reports what a
	// decode of that file reports.
	tcs := map[string]struct {
		err   error
		input string
		want  string
		opts  []niceyaml.DecodeOption
	}{
		"one problem": {
			input: "timeout: soon\n",
			want:  `base.yaml:1:10: $.timeout: time: invalid duration "soon"`,
		},
		"duration beside an unknown field": {
			input: "timeout: soon\nextra: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				`base.yaml:1:10: $.timeout: time: invalid duration "soon"`,
				`base.yaml:2:1: $.extra~: unknown field "extra"`,
			),
		},
		"text value beside a number out of range": {
			input: "tier: mid\nport: 70000\n",
			err:   errUnknownTier,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				`base.yaml:1:7: $.tier: unknown tier "mid"`,
				"base.yaml:2:7: $.port: expected integer from 0 to 65535, got 70000",
			),
		},
		// The rows come in the order of the file, with the rejection of
		// the decoder among them.
		"rejection after the other problems": {
			input: "extra: 1\nport: 70000\ntier: mid\ntimeout: soon\n",
			opts:  []niceyaml.DecodeOption{strict},
			err:   errUnknownTier,
			want: stringtest.JoinLF(
				"base.yaml: 4 problems",
				`base.yaml:1:1: $.extra~: unknown field "extra"`,
				"base.yaml:2:7: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:3:7: $.tier: unknown tier "mid"`,
				`base.yaml:4:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The merged text holds each value on a line of its own, at
		// another position than the file has for it. Each value still
		// reports once, at its place in the file.
		"flow mapping": {
			input: "{port: 70000, timeout: soon, extra: 1}\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:1:8: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:1:24: $.timeout: time: invalid duration "soon"`,
				`base.yaml:1:30: $.extra~: unknown field "extra"`,
			),
		},
		"flow mapping below a comment": {
			input: "# prod\n\n{tier: mid, port: 70000, timeout: soon}\n",
			err:   errUnknownTier,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				`base.yaml:3:8: $.tier: unknown tier "mid"`,
				"base.yaml:3:19: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:3:35: $.timeout: time: invalid duration "soon"`,
			),
		},
		"alias to a duration": {
			input: "wait: &wait soon\ntimeout: *wait\nport: 70000\n",
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				`base.yaml:2:10: $.timeout: time: invalid duration "soon"`,
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
			),
		},
		// The merged text leaves out the blank line of each input below,
		// so the unmarshaler reads another position than the file has for
		// its value.
		"unmarshaler error with no location": {
			input: "# prod\n\nport: 70000\nown: plain\ntimeout: soon\n",
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				"base.yaml:4:6: $.own: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		"unmarshaler error at a position": {
			input: "# prod\n\nport: 70000\nown: position\ntimeout: soon\n",
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				"base.yaml:4:6: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		"unmarshaler error at a range": {
			input: "# prod\n\nport: 70000\nown: range\ntimeout: soon\n",
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				"base.yaml:4:6: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The error names the port, so it stands for the problem the
		// search finds there.
		"unmarshaler error at the path of another problem": {
			input: "# prod\n\nport: 70000\nown: path\ntimeout: soon\n",
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				"base.yaml:3:7: $.port: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		"unmarshaler error from BindValue": {
			input: "# prod\n\nport: 70000\nown: unplaced\ntimeout: soon\n",
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				"base.yaml:3:7: $.port: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The error binds at the key of the mapping that lacks the key.
		"unmarshaler error at a path the document leaves out": {
			input: "# prod\n\nport: 70000\nown: absent\ntimeout: soon\n",
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:1: $.absent: bad server",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The panic binds at the first key of the mapping, so it stands
		// for the unknown field the search finds there. The search still
		// reports the duration, which matches ErrDecode beside the panic.
		"unmarshaler that panics": {
			input: "# prod\n\nextra: 1\nown: panic\ntimeout: soon\n",
			opts:  []niceyaml.DecodeOption{strict},
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				"base.yaml:3:1: decoder panicked: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// An error with no position comes back alone, since nothing tells
		// it apart from a problem the search finds.
		"unmarshaler error at a range no line holds": {
			input: "# prod\n\nport: 70000\nown: far\ntimeout: soon\n",
			err:   errBadServer,
			want:  "base.yaml: bad server",
		},
		"unmarshaler error at a path that resolves nowhere": {
			input: "# prod\n\nport: 70000\nown: below\ntimeout: soon\n",
			err:   errBadServer,
			want:  "base.yaml: $.port.unit: bad server",
		},
		"unmarshaler error at a path several keys share": {
			input: "# prod\n\nport: 70000\nown: ambiguous\ntimeout: soon\n",
			want:  "base.yaml: $.1.limit: limit must be at least 1",
		},
		// The position of the error lies in the other source, so nothing
		// joins it either.
		"unmarshaler error bound to another source": {
			input: "# prod\n\nport: 70000\nown: elsewhere\ntimeout: soon\n",
			err:   errBadServer,
			want:  "inner.yaml:1:7: $.port: bad server",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("base.yaml"))

			_, fromLayers := niceyaml.NewSourceFromLayers(doc).Decode[problemTimed](t.Context(), tc.opts...)
			_, fromSource := doc.Source().Decode[problemTimed](t.Context(), tc.opts...)

			for route, err := range map[string]error{"layers": fromLayers, "source": fromSource} {
				require.EqualError(t, err, tc.want, route)
				require.ErrorIs(t, err, niceyaml.ErrDecode, route)

				if tc.err != nil {
					require.ErrorIs(t, err, tc.err, route)
				}
			}

			want := decodeRows(doc, fromSource)
			assert.Equal(t, want, decodeRows(doc, fromLayers))
		})
	}
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
	assert.Empty(t, srcErr.Members())
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
	require.Len(t, srcErr.Members(), servers)

	lines := strings.Split(err.Error(), "\n")
	require.Len(t, lines, niceyaml.ErrorListLimit+2)
	assert.Equal(t, "500 problems", lines[0])
	assert.Equal(t, "2:11: $.servers[0].port: expected integer, got string", lines[1])
	assert.Equal(t, "and 490 more", lines[len(lines)-1])
}
