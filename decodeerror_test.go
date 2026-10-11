package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

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
	// The error tier reports for text it does not know.
	errUnknownTier = errors.New("unknown tier")

	// The error a span reports for a to below its from.
	errSpan = errors.New("to is below from")
)

// tier decodes itself from text and reports errUnknownTier for any text
// but "low" and "high".
type tier int

func (t *tier) UnmarshalText(text []byte) error {
	switch string(text) {
	case "low":
		*t = 1
	case "high":
		*t = 2
	default:
		return fmt.Errorf("%w %q", errUnknownTier, text)
	}

	return nil
}

// tierServer holds a value of each kind the decoder returns the error of
// with no token.
type tierServer struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
	Tier    tier          `yaml:"tier"`
}

// mirrored decodes itself through a second type with the same fields, so
// the error of a field comes back as the field reported it.
type mirrored struct {
	Name    string       `yaml:"name"`
	Servers []tierServer `yaml:"servers"`
}

func (m *mirrored) UnmarshalYAML(unmarshal func(any) error) error {
	type plain mirrored

	return unmarshal((*plain)(m))
}

// prefixing decodes itself through a second type and puts text of its
// own in front of the error, so no field below it reports that message.
type prefixing struct {
	Server tierServer
}

func (p *prefixing) UnmarshalYAML(unmarshal func(any) error) error {
	err := unmarshal(&p.Server)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	return nil
}

// opaqueValue decodes itself from YAML bytes and rejects any that hold
// the word bad, so its fields mirror nothing in the document.
type opaqueValue struct{}

func (*opaqueValue) UnmarshalYAML(data []byte) error {
	if strings.Contains(string(data), "bad") {
		return errUnmarshal
	}

	return nil
}

// jsonDecoded decodes itself from JSON, which the decoder hands it only
// under niceyaml.WithJSONUnmarshalers.
type jsonDecoded struct {
	V string `yaml:"v"`
}

func (*jsonDecoded) UnmarshalJSON([]byte) error {
	return errUnmarshal
}

// jsonFielded has an UnmarshalJSON method the decoder calls only under
// niceyaml.WithJSONUnmarshalers. Without that option the decoder reads
// its field.
type jsonFielded struct {
	Tier tier `yaml:"tier"`
}

func (*jsonFielded) UnmarshalJSON([]byte) error {
	return nil
}

// tierHolder decodes itself from text and holds a field that decodes
// from text too, which the decoder never reads.
type tierHolder struct {
	Tier tier `yaml:"tier"`
}

func (*tierHolder) UnmarshalText([]byte) error {
	return nil
}

// lockable decodes itself from text unless the value it decodes into is
// locked already, which only a value the caller filled can be.
type lockable struct {
	Text   string
	Locked bool
}

func (l *lockable) UnmarshalText(text []byte) error {
	if l.Locked {
		return errUnmarshal
	}

	l.Text = string(text)

	return nil
}

// rejectsOnce decodes itself by reporting errUnmarshal on the first call
// that the counter in the context of the decode counts, and by panicking
// on every call after it.
type rejectsOnce struct{}

func (*rejectsOnce) UnmarshalYAML(ctx context.Context, _ []byte) error {
	if calls, ok := ctx.Value(panicCallsKey{}).(*atomic.Int32); ok && calls.Add(1) == 1 {
		return errUnmarshal
	}

	panic("decoded twice")
}

// positioned decodes itself from its node and reports an error at the
// position of that node.
type positioned struct{}

func (*positioned) UnmarshalYAML(node ast.Node) error {
	return niceyaml.Invalid(errUnmarshal, niceyaml.AtPosition(position.NewFromToken(node.GetToken())))
}

// spanError returns errSpan at the path of the to, which reads from the
// value that reports it.
func spanError() error {
	return niceyaml.Invalid(errSpan, niceyaml.AtPath(paths.Current().Child("to")))
}

// span decodes itself through a second type with the same fields and
// reports a to below its from, as spanError returns it.
type span struct {
	From int `yaml:"from"`
	To   int `yaml:"to"`
}

func (s *span) UnmarshalYAML(unmarshal func(any) error) error {
	type plain span

	err := unmarshal((*plain)(s))
	if err != nil {
		return err
	}

	if s.To < s.From {
		return spanError()
	}

	return nil
}

// spanNode decodes itself from its node and reports spanError for every
// node.
type spanNode struct{}

func (*spanNode) UnmarshalYAML(ast.Node) error {
	return spanError()
}

// spanBytes decodes itself from YAML bytes and reports spanError for
// every node.
type spanBytes struct{}

func (*spanBytes) UnmarshalYAML(context.Context, []byte) error {
	return spanError()
}

// spanMirror decodes itself through a second type with the same fields,
// so the error of its span comes back as the span reported it.
type spanMirror struct {
	Name string `yaml:"name"`
	Span span   `yaml:"span"`
}

func (m *spanMirror) UnmarshalYAML(unmarshal func(any) error) error {
	type plain spanMirror

	return unmarshal((*plain)(m))
}

// spanWrapper decodes itself as [spanMirror] does and puts text of its
// own in front of the error of its span.
type spanWrapper struct {
	Span span `yaml:"span"`
}

func (w *spanWrapper) UnmarshalYAML(unmarshal func(any) error) error {
	type plain spanWrapper

	err := unmarshal((*plain)(w))
	if err != nil {
		return fmt.Errorf("wrapper: %w", err)
	}

	return nil
}

// spanRebaser decodes itself as [spanMirror] does and puts the error of
// its span under the path of that field itself.
type spanRebaser struct {
	Span span `yaml:"span"`
}

func (r *spanRebaser) UnmarshalYAML(unmarshal func(any) error) error {
	type plain spanRebaser

	//nolint:wrapcheck // The test inspects the error as it is.
	return niceyaml.Rebase(unmarshal((*plain)(r)), paths.Current().Child("span"))
}

// tierNamer decodes itself through a second type with the same fields
// and names its tier in the error of that tier, at a path of its own.
type tierNamer struct {
	Tier tier `yaml:"tier"`
}

func (n *tierNamer) UnmarshalYAML(unmarshal func(any) error) error {
	type plain tierNamer

	return niceyaml.Invalid(unmarshal((*plain)(n)), niceyaml.AtPath(paths.Current().Child("tier")))
}

// reportKey is the context key of the error a [reporting] returns.
type reportKey struct{}

// reporting decodes itself from any value and reports the error the
// context of the decode holds under reportKey.
type reporting struct{}

func (*reporting) UnmarshalYAML(ctx context.Context, _ func(any) error) error {
	err, ok := ctx.Value(reportKey{}).(error)
	if !ok {
		return nil
	}

	return err
}

// rebasing decodes itself through a second type with the same fields
// and puts the error of its item under the path of that field itself.
type rebasing struct {
	Item reporting `yaml:"item"`
}

func (r *rebasing) UnmarshalYAML(unmarshal func(any) error) error {
	type plain rebasing

	//nolint:wrapcheck // The test inspects the error as it is.
	return niceyaml.Rebase(unmarshal((*plain)(r)), paths.Current().Child("item"))
}

// optionDecoded has no method of its own. A function from
// niceyaml.WithCustomUnmarshaler decodes it.
type optionDecoded struct{}

// registeredDecoded has no method of its own. A function from
// yaml.RegisterCustomUnmarshaler decodes it in every decode of the
// program.
type registeredDecoded struct{}

// cancelKey is the context key of the function canceling calls.
type cancelKey struct{}

// canceling decodes itself with the context of the decode, which it ends
// before it reports errUnmarshal.
type canceling struct{}

func (*canceling) UnmarshalYAML(ctx context.Context, _ []byte) error {
	cancel, ok := ctx.Value(cancelKey{}).(context.CancelFunc)
	if ok {
		cancel()
	}

	return errUnmarshal
}

// rejectionServer is an element of [rejectionConfig].
type rejectionServer struct {
	Host  string `yaml:"host"`
	Port  int    `yaml:"port"`
	Small int8   `yaml:"small"`
	Wide  uint16 `yaml:"wide"`
}

// rejectionConfig holds a field of each kind the go-yaml decoder rejects
// a value for with a token of the source.
type rejectionConfig struct {
	When    time.Time         `yaml:"when"`
	Labels  map[string]string `yaml:"labels"`
	Counts  map[int]int       `yaml:"counts"`
	Name    string            `yaml:"name"`
	Servers []rejectionServer `yaml:"servers"`
	One     rejectionServer   `yaml:"one"`
	Top     int               `yaml:"top"`
	Ratio   float64           `yaml:"ratio"`
	Wait    time.Duration     `yaml:"wait"`
	On      bool              `yaml:"on"`
}

// requireNamedPath returns the path srcErr carries, after checking that
// want, the message of the error, names that path behind its position.
func requireNamedPath(t *testing.T, srcErr *niceyaml.SourceError, want string) paths.Path {
	t.Helper()

	path, ok := srcErr.Path()
	require.True(t, ok, "the error carries no path")
	assert.Contains(t, want, ": "+path.String()+": ")

	return path
}

func TestDocument_Decode_Rejection(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		opts  []niceyaml.DecodeOption
	}{
		"scalar in an element": {
			input: stringtest.LinesLF(
				"servers:",
				"  - host: h",
				"    port: abc",
			),
			want: "3:11: $.servers[0].port: expected integer, got string",
		},
		"block mapping for an integer": {
			input: stringtest.LinesLF(
				"top:",
				"  a: 1",
				"  b: 2",
			),
			want: "1:1: $.top: expected integer, got mapping",
		},
		"block mapping for a string in a map": {
			input: stringtest.LinesLF(
				"labels:",
				"  a:",
				"    b: 1",
			),
			want: "2:3: $.labels.a: expected string, got mapping",
		},
		"block sequence for an integer": {
			input: stringtest.LinesLF(
				"top:",
				"  - 1",
				"  - 2",
			),
			want: "1:1: $.top: expected integer, got sequence",
		},
		"flow sequence for a string": {
			input: "name: [a, b]\n",
			want:  "1:1: $.name: expected string, got sequence",
		},
		"flow mapping for an integer": {
			input: "top: {a: 1}\n",
			want:  "1:1: $.top: expected integer, got mapping",
		},
		"empty flow sequence for an integer": {
			input: "top: []\n",
			want:  "1:1: $.top: expected integer, got sequence",
		},
		"string for a sequence": {
			input: "servers: abc\n",
			want:  "1:10: $.servers: expected sequence, got string",
		},
		"string for a mapping": {
			input: "one: abc\n",
			want:  "1:6: $.one: expected mapping, got string",
		},
		"block sequence for a mapping": {
			input: stringtest.LinesLF("one:", "  - a"),
			want:  "1:1: $.one: expected mapping, got sequence",
		},
		"sequence element for a mapping": {
			input: stringtest.LinesLF("servers:", "  - - a"),
			want:  "2:3: $.servers[0]: expected mapping, got sequence",
		},
		"integer for a sequence": {
			input: "servers: 5\n",
			want:  "1:10: $.servers: expected sequence, got integer",
		},
		"boolean for a mapping": {
			input: "one: true\n",
			want:  "1:6: $.one: expected mapping, got boolean",
		},
		"block scalar for a mapping": {
			input: stringtest.LinesLF("one: |", "  abc"),
			want:  "1:6: $.one: expected mapping, got string",
		},
		"anchored scalar": {
			input: "top: &t abc\n",
			want:  "1:9: $.top: expected integer, got string",
		},
		"tagged scalar": {
			input: "top: !!str abc\n",
			want:  "1:12: $.top: expected integer, got string",
		},
		"value an alias reads": {
			input: stringtest.LinesLF(
				"defs:",
				"  p: &p abc",
				"servers:",
				"  - port: *p",
			),
			want: "2:9: $.defs.p: expected integer, got string",
		},
		"value a merge key brings in": {
			input: stringtest.LinesLF(
				"defs: &d",
				"  port: abc",
				"servers:",
				"  - <<: *d",
				"    host: h",
			),
			want: "2:9: $.defs.port: expected integer, got string",
		},
		"map key": {
			input: stringtest.LinesLF("counts:", "  x: 1"),
			want:  "2:3: $.counts.x~: expected integer, got string",
		},
		"explicit map key": {
			input: stringtest.LinesLF(
				"counts:",
				"  ? x",
				"  : 1",
			),
			want: "2:5: $.counts.x~: expected integer, got string",
		},
		"map value": {
			input: stringtest.LinesLF("counts:", "  1: x"),
			want:  "2:6: $.counts.1: expected integer, got string",
		},
		"key a path quotes": {
			input: stringtest.LinesLF("labels:", `  "a.b": [1]`),
			want:  "2:3: $.labels.'a.b': expected string, got sequence",
		},
		"overflow of a signed integer": {
			input: stringtest.LinesLF("servers:", "  - small: 300"),
			want:  "2:12: $.servers[0].small: expected integer from -128 to 127, got 300",
		},
		"negative for an unsigned integer": {
			input: stringtest.LinesLF("servers:", "  - wide: -1"),
			want:  "2:11: $.servers[0].wide: expected integer from 0 to 65535, got -1",
		},
		"overflow of an unsigned integer": {
			input: "one: {wide: 70000}\n",
			want:  "1:13: $.one.wide: expected integer from 0 to 65535, got 70000",
		},
		"float past an integer": {
			input: "one: {small: 1e10}\n",
			want:  "1:14: $.one.small: expected integer from -128 to 127, got 1e10",
		},
		"boolean for an integer": {
			input: "top: true\n",
			want:  "1:6: $.top: expected integer, got boolean",
		},
		"integer for a boolean": {
			input: "on: 5\n",
			want:  "1:5: $.on: expected boolean, got integer",
		},
		"mapping for a float": {
			input: "ratio: {a: 1}\n",
			want:  "1:1: $.ratio: expected float, got mapping",
		},
		"sequence for a timestamp": {
			input: "when: [1]\n",
			want:  "1:1: $.when: expected timestamp, got sequence",
		},
		"sequence for a duration": {
			input: "wait: [1]\n",
			want:  "1:1: $.wait: expected duration, got sequence",
		},
		"tagged null for an integer": {
			input: "top: !!null x\n",
			want:  "1:13: $.top: expected integer, got null",
		},
		"binary for an integer": {
			input: "top: !!binary YWJj\n",
			want:  "1:15: $.top: expected integer, got binary",
		},
		"timestamp for an integer": {
			input: "top: !!timestamp 2020-01-01\n",
			want:  "1:18: $.top: expected integer, got timestamp",
		},
		"ordered mapping for an integer": {
			input: "top: {a: 1}\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
			want:  "1:1: $.top: expected integer, got mapping",
		},
		"unknown field": {
			input: "name: x\nfoo: 1\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  `2:1: $.foo~: unknown field "foo"`,
		},
		"unknown field in an element": {
			input: stringtest.LinesLF("servers:", "  - zzz: 1"),
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  `2:5: $.servers[0].zzz~: unknown field "zzz"`,
		},
		"unknown field a merge key brings in": {
			input: stringtest.LinesLF(
				"labels: &d",
				"  zzz: 1",
				"one:",
				"  <<: *d",
			),
			opts: []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want: `2:3: $.labels.zzz~: unknown field "zzz"`,
		},
		"alias with no anchor": {
			input: "top: *nope\n",
			want:  `1:6: $.top: could not find alias "nope"`,
		},
		"value under renamed anchors": {
			input: stringtest.LinesLF(
				"a: &x 1",
				"b: &x 2",
				"top: abc",
			),
			want: "3:6: $.top: expected integer, got string",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			var v rejectionConfig

			err := dd.DecodeInto(t.Context(), &v, tc.opts...)
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			srcErr := yamltest.RequireSourceError(t, err)
			require.NoError(t, srcErr.Unresolved())

			path := requireNamedPath(t, srcErr, tc.want)

			// The message names no Go type or field.
			for _, word := range []string{"unmarshal", "Go ", "struct", "interface"} {
				assert.NotContains(t, srcErr.Message(), word)
			}

			// The rejection binds where an error at its path binds, as one
			// from a validator does.
			atPath := yamltest.RequireSourceError(t, dd.Bind(niceyaml.NewError("at the path", niceyaml.AtPath(path))))
			require.NoError(t, atPath.Unresolved())

			_, near := atPath.Nearest()
			require.False(t, near, "the path selects nothing")

			want, ok := atPath.Range()
			require.True(t, ok)

			got, ok := srcErr.Range()
			require.True(t, ok)
			assert.Equal(t, want, got)

			// The position of the error lies at the path: on the key for
			// the path of a key, and at or inside the value for any other.
			pos, ok := srcErr.Position()
			require.True(t, ok)

			under, ok := dd.PathAt(pos)
			require.True(t, ok)

			if strings.HasSuffix(path.String(), "~") {
				assert.Equal(t, path, under)

				return
			}

			_, ok = under.CutPrefix(path)
			assert.True(t, ok, "position %s lies at %s", pos, under)
		})
	}
}

func TestDocument_Decode_Rejection_GoTypes(t *testing.T) {
	t.Parallel()

	// An inline field that takes the value of the anchor a `<<` key names.
	// The decoder assigns the value as it decoded the anchor, and rejects
	// one of any Go type but the type of the field.
	type aliased struct {
		*StrictBase `yaml:",inline,alias"`

		B int `yaml:"b"`
	}

	type other struct {
		A int `yaml:"a"`
	}

	merge := stringtest.LinesLF(
		"defs: &d",
		"  a: 1",
		"v:",
		"  <<: *d",
		"  b: 1",
	)

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		input  string
		want   string
	}{
		"channel": {
			decode: decodeAs[struct {
				V chan int `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		"pointer to a channel": {
			decode: decodeAs[struct {
				V *chan int `yaml:"v"`
			}](),
			input: "v: {a: 1}\n",
			want:  "1:1: $.v: expected no value, got mapping",
		},
		"function": {
			decode: decodeAs[struct {
				V func() `yaml:"v"`
			}](),
			input: "v: abc\n",
			want:  "1:4: $.v: expected no value, got string",
		},
		"complex number": {
			decode: decodeAs[struct {
				V complex128 `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		"element of an array of complex numbers": {
			decode: decodeAs[struct {
				V [2]complex64 `yaml:"v"`
			}](),
			input: "v: [1.5]\n",
			want:  "1:5: $.v[0]: expected no value, got float",
		},
		"value of a map of functions": {
			decode: decodeAs[struct {
				V map[string]func() `yaml:"v"`
			}](),
			input: "v: {a: true}\n",
			want:  "1:8: $.v.a: expected no value, got boolean",
		},
		"unsafe pointer": {
			decode: decodeAs[struct {
				V unsafe.Pointer `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		// The decoder fills one pointer around a value and no more.
		"pointer to a pointer": {
			decode: decodeAs[struct {
				V **int `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected integer, got integer of another type",
		},
		"anchor read into an interface": {
			decode: decodeAs[struct {
				Defs any     `yaml:"defs"`
				V    aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got value of another type",
		},
		"anchor read into another struct": {
			decode: decodeAs[struct {
				Defs *other  `yaml:"defs"`
				V    aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got mapping of another type",
		},
		"anchor read into a map": {
			decode: decodeAs[struct {
				Defs map[string]int `yaml:"defs"`
				V    aliased        `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got mapping of another type",
		},
		"anchor read into no field": {
			decode: decodeAs[struct {
				V aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got mapping of another type",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := tc.decode(t.Context(), dd)
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			// The decoder reports the two Go types it compared, and the
			// message spells neither of them.
			var typeErr *yaml.TypeError

			require.ErrorAs(t, err, &typeErr)

			srcErr := yamltest.RequireSourceError(t, err)

			for _, typ := range []reflect.Type{typeErr.DstType, typeErr.SrcType} {
				// The kinds integer, string, and boolean hold the names of
				// the Go types int, string, and bool.
				if spelled := typ.String(); !slices.Contains([]string{"int", "string", "bool"}, spelled) {
					assert.NotContains(t, srcErr.Message(), spelled)
				}
			}

			for _, word := range []string{"chan", "func", "complex", "unsafe", "interface", "struct", "map[", "*", "niceyaml"} {
				assert.NotContains(t, srcErr.Message(), word)
			}
		})
	}
}

func TestDocument_Decode_Rejection_GoYAMLError(t *testing.T) {
	t.Parallel()

	// The message reads in YAML terms, and [errors.As] still finds the
	// error the decoder returned, with its Go types.
	t.Run("type mismatch", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "top: abc\n").DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "1:6: $.top: expected integer, got string")

		got, ok := errors.AsType[*yaml.TypeError](err)
		require.True(t, ok)
		assert.Equal(t, reflect.TypeFor[int](), got.DstType)
		assert.Equal(t, reflect.TypeFor[string](), got.SrcType)
	})

	t.Run("overflow", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "one: {small: 300}\n").DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "1:14: $.one.small: expected integer from -128 to 127, got 300")

		got, ok := errors.AsType[*yaml.OverflowError](err)
		require.True(t, ok)
		assert.Equal(t, reflect.TypeFor[int8](), got.DstType)
		assert.Equal(t, "300", got.SrcNum)
	})

	t.Run("unexpected node", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "servers: abc\n").DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "1:10: $.servers: expected sequence, got string")

		got, ok := errors.AsType[*yaml.UnexpectedNodeTypeError](err)
		require.True(t, ok)
		assert.Equal(t, ast.SequenceType, got.Expected)
		assert.Equal(t, ast.StringType, got.Actual)
	})

	t.Run("unknown field", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "foo: 1\n").
			DecodeInto(t.Context(), &v, niceyaml.WithDisallowUnknownFields(true))
		require.EqualError(t, err, `1:1: $.foo~: unknown field "foo"`)

		got, ok := errors.AsType[*yaml.UnknownFieldError](err)
		require.True(t, ok)
		assert.Equal(t, `unknown field "foo"`, got.GetMessage())
	})
}

// requireSentinels checks that err matches each sentinel in want, and
// none of the other sentinels a decode or the parse reports a problem
// with.
func requireSentinels(t *testing.T, err error, want ...error) {
	t.Helper()

	sentinels := []error{
		niceyaml.ErrDecode,
		niceyaml.ErrSyntax,
		niceyaml.ErrUnknownField,
		niceyaml.ErrWrongKind,
		niceyaml.ErrOverflow,
		niceyaml.ErrOutOfRange,
	}

	for _, sentinel := range sentinels {
		if slices.Contains(want, sentinel) {
			require.ErrorIs(t, err, sentinel)
		} else {
			assert.NotErrorIs(t, err, sentinel)
		}
	}
}

func TestDocument_Decode_Rejection_Label(t *testing.T) {
	t.Parallel()

	// The target holds a field of each kind whose rejection a decode
	// labels, and of each kind whose error it gives no label.
	type target struct {
		Addr   netip.Addr               `yaml:"addr"`
		Bare   bareReparsingUnmarshaler `yaml:"bare"`
		Deep   **int                    `yaml:"deep"`
		Self   opaqueValue              `yaml:"self"`
		Option optionDecoded            `yaml:"option"`
		One    rejectionServer          `yaml:"one"`
		Wait   time.Duration            `yaml:"wait"`
		Ratio  float64                  `yaml:"ratio"`
		Top    int                      `yaml:"top"`
		Tier   tier                     `yaml:"tier"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The function decodes the value as an int8 and puts text of its own
	// in front of the rejection.
	wrapping := niceyaml.WithCustomUnmarshaler(
		func(_ context.Context, _ *optionDecoded, decode func(any) error) error {
			var small int8

			err := decode(&small)
			if err != nil {
				return fmt.Errorf("option: %w", err)
			}

			return nil
		},
	)

	refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {small: 300}\n"))

	tcs := map[string]struct {
		input string
		// The message of the one problem the decode reports.
		want string
		// The sentinels that problem matches.
		err    []error
		opts   []niceyaml.DecodeOption
		source []niceyaml.SourceOption
	}{
		"unknown field": {
			input: "other: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want:  `unknown field "other"`,
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrUnknownField},
		},
		"scalar for a mapping": {
			input: "one: abc\n",
			want:  "expected mapping, got string",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"mapping for a scalar": {
			input: "top: {a: 1}\n",
			want:  "expected integer, got mapping",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		// The decode words this rejection itself, so its chain holds no
		// go-yaml error to name its kind.
		"sequence for a value that decodes from text": {
			input: "addr: [1]\n",
			want:  "expected string, got sequence",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		// The decoder reports a type error for a number no float64 holds.
		"number too large for a float": {
			input: "ratio: 1e400\n",
			want:  "expected float, got string",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"value of another type": {
			input: "deep: 1\n",
			want:  "expected integer, got integer of another type",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"number past the range of an int8": {
			input: "one: {small: 300}\n",
			want:  "expected integer from -128 to 127, got 300",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		// The decoder reports an overflow for an infinity in an integer.
		"infinity for an integer": {
			input: "one: {small: .inf}\n",
			want:  "expected integer from -128 to 127, got +Inf",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		"number an alias reads from a reference document": {
			input:  "one: *base\n",
			source: []niceyaml.SourceOption{refs},
			want:   "expected integer from -128 to 127, got 300",
			err:    []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		"rejection a custom unmarshaler wraps": {
			input: "option: 300\n",
			opts:  []niceyaml.DecodeOption{wrapping},
			want:  "option: expected integer from -128 to 127, got 300",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		"error of an UnmarshalYAML method": {
			input: "self: bad\n",
			want:  "unmarshaler rejected the value",
			err:   []error{niceyaml.ErrDecode},
		},
		"error of an UnmarshalText method": {
			input: "tier: mid\n",
			want:  `unknown tier "mid"`,
			err:   []error{niceyaml.ErrDecode},
		},
		"duration that does not parse": {
			input: "wait: soon\n",
			want:  `time: invalid duration "soon"`,
			err:   []error{niceyaml.ErrDecode},
		},
		"alias with no anchor": {
			input: "top: *nope\n",
			want:  `could not find alias "nope"`,
			err:   []error{niceyaml.ErrDecode},
		},
		"syntax error": {
			input: "top: [1\n",
			want:  "sequence end token ']' not found",
			err:   []error{niceyaml.ErrSyntax},
		},
		"duplicate key": {
			input: "top: 1\ntop: 2\n",
			want:  `mapping key "top" already defined at [1:1]`,
			err:   []error{niceyaml.ErrSyntax},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := niceyaml.NewSourceFromString(tc.input, tc.source...).Decode[target](t.Context(), tc.opts...)
			require.Error(t, err)

			problems := slices.Collect(niceyaml.Problems(err))
			require.Len(t, problems, 1)
			assert.Equal(t, tc.want, problems[0].Message())
			requireSentinels(t, problems[0].Err, tc.err...)
		})
	}

	// The method returns the error of a go-yaml decode of its bytes, with
	// go-yaml's text. The message decides the label, so the go-yaml error
	// in the chain gives the problem none.
	t.Run("go-yaml error of an UnmarshalYAML method", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString("bare: {name: abc}\n").Decode[target](t.Context())
		require.Error(t, err)

		problems := slices.Collect(niceyaml.Problems(err))
		require.Len(t, problems, 1)
		requireSentinels(t, problems[0].Err, niceyaml.ErrDecode)
		require.ErrorAs(t, problems[0].Err, new(*yaml.UnexpectedNodeTypeError))
	})

	// A summary matches each label that one of its problems matches, and
	// a problem matches no label but its own.
	t.Run("several problems", func(t *testing.T) {
		t.Parallel()

		input := stringtest.LinesLF(
			"other: 1",
			"top: abc",
			"one: {small: 300}",
			"addr: [1]",
			"wait: soon",
		)

		_, err := niceyaml.NewSourceFromString(input).Decode[target](t.Context(), strict)
		require.Error(t, err)
		requireSentinels(t, err,
			niceyaml.ErrDecode, niceyaml.ErrUnknownField, niceyaml.ErrWrongKind, niceyaml.ErrOverflow)

		want := [][]error{
			{niceyaml.ErrDecode, niceyaml.ErrUnknownField},
			{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
			{niceyaml.ErrDecode, niceyaml.ErrOverflow},
			{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
			{niceyaml.ErrDecode},
		}

		problems := slices.Collect(niceyaml.Problems(err))
		require.Len(t, problems, len(want))

		for i, problem := range problems {
			requireSentinels(t, problem.Err, want[i]...)
		}
	})
}

func TestDocument_Decode_Rejection_Scoped(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`
		defs: &d
		  port: abc
		servers:
		  - host: h
		    port: xyz
		  - <<: *d
	`), "s.yaml")

	tcs := map[string]struct {
		scope string
		want  string
	}{
		// The path starts at `$`, so the scope of the Node does not go
		// in front of it a second time.
		"value inside the scope": {
			scope: "$.servers[0]",
			want:  "s.yaml:5:11: $.servers[0].port: expected integer, got string",
		},
		// The merge key brings the value in from outside the scope,
		// and the path names the place where the anchor defines it.
		"value a merge key brings in from outside the scope": {
			scope: "$.servers[1]",
			want:  "s.yaml:2:9: $.defs.port: expected integer, got string",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scoped := yamltest.At(t, doc, paths.MustParse(tc.scope))

			_, err := scoped.Decode[rejectionServer](t.Context())
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			srcErr := yamltest.RequireSourceError(t, err)

			path := requireNamedPath(t, srcErr, tc.want)

			// The error stays bound to the Node that decoded, and its
			// path resolves from the root of the document.
			assert.Same(t, scoped, yamltest.BoundNode(t, srcErr))
			assert.Same(t, doc, yamltest.BoundDocument(t, srcErr))

			ranges, err := yamltest.BoundDocument(t, srcErr).Ranges(path)
			require.NoError(t, err)
			require.Len(t, ranges, 1)

			pos, ok := srcErr.Position()
			require.True(t, ok)
			assert.Equal(t, ranges[0].Start, pos)
		})
	}
}

func TestDocument_Decode_Rejection_Unselected(t *testing.T) {
	t.Parallel()

	t.Run("path that selects another entry", func(t *testing.T) {
		t.Parallel()

		// The document may hold the key twice, and a decode into a map
		// reads every entry, so the decoder rejects the value of the
		// second. A path through the key selects the third, so the error
		// binds at the token the decoder reported, and the path names
		// the value in the message.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"name: 1",
			"name: y",
			"name: 3",
		), niceyaml.WithAllowDuplicateKeys(true))

		_, err := doc.Decode[map[string]int](t.Context())
		require.EqualError(t, err, "2:7: $.name: expected integer, got string")
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)
		require.NoError(t, srcErr.Unresolved())

		path, ok := srcErr.Path()
		require.True(t, ok)
		assert.Equal(t, "$.name", path.String())

		pos, ok := srcErr.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 6), pos)

		ranges, err := doc.Ranges(path)
		require.NoError(t, err)
		require.Len(t, ranges, 1)
		assert.Equal(t, position.New(2, 6), ranges[0].Start)

		_, ok = doc.PathAt(pos)
		assert.False(t, ok, "a path selects the second entry")
	})

	t.Run("key with no name", func(t *testing.T) {
		t.Parallel()

		// An alias key that names no anchor has no name, so no path
		// spells its entry and the rejection carries the position alone.
		doc := yamltest.FirstDocument(t, "b: 2\n*nope : 1\n")

		_, err := doc.Decode[map[string]int](t.Context())
		require.EqualError(t, err, `2:2: could not find alias "nope"`)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)

		_, ok := srcErr.Path()
		assert.False(t, ok)

		pos, ok := srcErr.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 1), pos)
	})
}

func TestDocument_Decode_UnmarshalerError(t *testing.T) {
	t.Parallel()

	// A server whose tier names no tier.
	const midTier = "name: api\ntier: mid\n"

	// A list of one span whose to lies below its from.
	oneSpan := stringtest.LinesLF(
		"spans:",
		"  - from: 9",
		"    to: 3",
	)

	t.Run("binds at the value that reported it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
			// The path of the node that decodes, for a case that decodes
			// below the root.
			scope paths.Path
		}{
			"duration field": {
				input:  "name: api\ntimeout: soon\n",
				decode: decodeAs[tierServer](),
				want:   `2:10: $.timeout: time: invalid duration "soon"`,
			},
			"text unmarshaler in a sequence of structs": {
				input: stringtest.LinesLF(
					"- name: a",
					"  tier: low",
					"- name: b",
					"  tier: mid",
				),
				decode: decodeAs[[]tierServer](),
				want:   `4:9: $[1].tier: unknown tier "mid"`,
			},
			"pointer element": {
				input: "tiers: [low, mid]\n",
				decode: decodeAs[struct {
					Tiers []*tier `yaml:"tiers"`
				}](),
				want: `1:14: $.tiers[1]: unknown tier "mid"`,
			},
			"map key": {
				input: stringtest.LinesLF(
					"hosts:",
					"  10.0.0.1: a",
					"  nope: b",
				),
				decode: decodeAs[struct {
					Hosts map[netip.Addr]string `yaml:"hosts"`
				}](),
				want: `3:3: $.hosts.nope~: ParseAddr("nope"): unable to parse IP`,
			},
			"map value": {
				input:  "a: low\nb: mid\n",
				decode: decodeAs[map[string]tier](),
				want:   `2:4: $.b: unknown tier "mid"`,
			},
			// The path through a merge key resolves to the entry of the
			// mapping the key brings in.
			"map value a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base",
					"  timeout: soon",
					"servers:",
					"  api:",
					"    <<: *base",
				),
				decode: decodeAs[struct {
					Servers map[string]tierServer `yaml:"servers"`
				}](),
				want: `2:12: $.servers.api.timeout: time: invalid duration "soon"`,
			},
			"map entry a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base",
					"  b: mid",
					"tiers:",
					"  a: low",
					"  <<: *base",
				),
				decode: decodeAs[struct {
					Tiers map[string]tier `yaml:"tiers"`
				}](),
				want: `2:6: $.tiers.b: unknown tier "mid"`,
			},
			// The path of a value an alias holds points at the alias.
			"alias": {
				input: stringtest.LinesLF("label: &label mid", "tiers: [low, *label]"),
				decode: decodeAs[struct {
					Label string `yaml:"label"`
					Tiers []tier `yaml:"tiers"`
				}](),
				want: `2:14: $.tiers[1]: unknown tier "mid"`,
			},
			"inline struct": {
				input: stringtest.LinesLF(
					"id: 1",
					"name: api",
					"tier: mid",
				),
				decode: decodeAs[struct {
					Server tierServer `yaml:",inline"`
					ID     int        `yaml:"id"`
				}](),
				want: `3:7: $.tier: unknown tier "mid"`,
			},
			"inline map": {
				input: "id: low\nextra: mid\n",
				decode: decodeAs[struct {
					Tiers map[string]tier `yaml:",inline"`
					ID    string          `yaml:"id"`
				}](),
				want: `2:8: $.extra: unknown tier "mid"`,
			},
			// The decoder reads the fields of a struct in the order the
			// struct declares them, so the first field that fails reports
			// the error, wherever the document puts it. The search for the
			// other problems of the decode reads no opaqueValue, so the
			// error comes back alone.
			"first field in struct order": {
				input: "value: bad\ntimeout: soon\n",
				decode: decodeAs[struct {
					Timeout time.Duration `yaml:"timeout"`
					Value   opaqueValue   `yaml:"value"`
				}](),
				want: `2:10: $.timeout: time: invalid duration "soon"`,
			},
			"value that decodes through a second type": {
				input: stringtest.LinesLF(
					"name: prod",
					"servers:",
					"  - name: a",
					"    tier: low",
					"  - name: b",
					"    tier: mid",
				),
				decode: decodeAs[mirrored](),
				want:   `6:11: $.servers[1].tier: unknown tier "mid"`,
			},
			"field that decodes through a second type": {
				input: stringtest.LinesLF(
					"prod:",
					"  servers:",
					"    - timeout: soon",
				),
				decode: decodeAs[struct {
					Prod mirrored `yaml:"prod"`
				}](),
				want: `3:16: $.prod.servers[0].timeout: time: invalid duration "soon"`,
			},
			// No field below the value reports the message with the text
			// the value put in front, so the error points at the value.
			"value that adds text to the error of a field": {
				input: stringtest.LinesLF(
					"main:",
					"  name: api",
					"  tier: mid",
				),
				decode: decodeAs[struct {
					Main prefixing `yaml:"main"`
				}](),
				want: `1:1: $.main: server: unknown tier "mid"`,
			},
			"value whose fields mirror nothing": {
				input: stringtest.LinesLF(
					"name: api",
					"value:",
					"  a: 1",
					"  b: bad",
				),
				decode: decodeAs[struct {
					Name  string      `yaml:"name"`
					Value opaqueValue `yaml:"value"`
				}](),
				want: `2:1: $.value: unmarshaler rejected the value`,
			},
			"json unmarshaler under its option": {
				input: stringtest.LinesLF(
					"name: api",
					"value:",
					"  v: 1",
				),
				decode: decodeAs[struct {
					Name  string      `yaml:"name"`
					Value jsonDecoded `yaml:"value"`
				}](niceyaml.WithJSONUnmarshalers(true)),
				want: `2:1: $.value: unmarshaler rejected the value`,
			},
			"type an option decodes": {
				input: "name: api\nvalue: bad\n",
				decode: decodeAs[struct {
					Name  string        `yaml:"name"`
					Value optionDecoded `yaml:"value"`
				}](niceyaml.WithCustomUnmarshaler(
					func(context.Context, *optionDecoded, func(any) error) error { return errUnmarshal },
				)),
				want: `2:8: $.value: unmarshaler rejected the value`,
			},
			// The decoder reads the value field by field, so the field
			// reported the error.
			"json unmarshaler without its option": {
				input: stringtest.LinesLF(
					"name: api",
					"value:",
					"  tier: mid",
				),
				decode: decodeAs[struct {
					Name  string      `yaml:"name"`
					Value jsonFielded `yaml:"value"`
				}](),
				want: `3:9: $.value.tier: unknown tier "mid"`,
			},
			// The scalar the decode reads is the value, so the error
			// points at the node itself, which the path names from the
			// root of the document.
			"scalar the decode reads": {
				input:  midTier,
				scope:  paths.Current().Child("tier"),
				decode: decodeAs[tier](),
				want:   `2:7: $.tier: unknown tier "mid"`,
			},
			"value below a scoped node": {
				input: stringtest.LinesLF(
					"servers:",
					"  - name: a",
					"    tier: mid",
				),
				scope:  paths.Current().Child("servers").Index(0),
				decode: decodeAs[tierServer](),
				want:   `3:11: $.servers[0].tier: unknown tier "mid"`,
			},
			"DecodeInto": {
				input: midTier,
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v tierServer

					return dd.DecodeInto(ctx, &v)
				},
				want: `2:7: $.tier: unknown tier "mid"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input), tc.scope)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)

				srcErr := yamltest.RequireSourceError(t, err)

				requireNamedPath(t, srcErr, tc.want)

				_, ok := srcErr.Range()
				assert.True(t, ok, "the error carries no location")
			})
		}
	})

	// The decoder hands an UnmarshalText method a scalar alone, and words
	// its refusal of any other node for neither the value nor its kind.
	t.Run("reads a mapping or a sequence in a text type as a string field does", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
		}{
			"sequence element": {
				input: stringtest.LinesLF(
					"addrs:",
					"  - 10.0.0.1",
					"  - [a, b]",
				),
				decode: decodeAs[struct {
					Addrs []netip.Addr `yaml:"addrs"`
				}](),
				want: "3:3: $.addrs[1]: expected string, got sequence",
			},
			"mapping field": {
				input: "name: api\naddr: {a: b}\n",
				decode: decodeAs[struct {
					Name string     `yaml:"name"`
					Addr netip.Addr `yaml:"addr"`
				}](),
				want: "2:1: $.addr: expected string, got mapping",
			},
			"pointer field": {
				input: stringtest.LinesLF("tier:", "  a: b"),
				decode: decodeAs[struct {
					Tier *tier `yaml:"tier"`
				}](),
				want: "1:1: $.tier: expected string, got mapping",
			},
			"map value": {
				input:  "a: low\nb: [mid]\n",
				decode: decodeAs[map[string]tier](),
				want:   "2:1: $.b: expected string, got sequence",
			},
			// The path of a value an alias holds points at the alias.
			"alias to a sequence": {
				input: "all: &all [low, high]\ntier: *all\n",
				decode: decodeAs[struct {
					Tier tier `yaml:"tier"`
				}](),
				want: "2:7: $.tier: expected string, got sequence",
			},
			// The decoder reads nothing below the mapping, so the tier in
			// it reports nothing.
			"mapping that holds a sequence": {
				input: stringtest.LinesLF("holder:", "  tier: [a]"),
				decode: decodeAs[struct {
					Holder tierHolder `yaml:"holder"`
				}](),
				want: "1:1: $.holder: expected string, got mapping",
			},
			// The decoder calls the UnmarshalJSON method of the type only
			// under its option.
			"type with an UnmarshalJSON method too": {
				input: "either: {a: 1}\n",
				decode: decodeAs[struct {
					Either problemTextual `yaml:"either"`
				}](),
				want: "1:1: $.either: expected string, got mapping",
			},
			"sequence the decode reads": {
				input:  "[a, b]\n",
				decode: decodeAs[tier](),
				want:   "1:1: $: expected string, got sequence",
			},
			"empty mapping the decode reads": {
				input:  "{}\n",
				decode: decodeAs[*tier](),
				want:   "1:1: $: expected string, got mapping",
			},
			"mapping a scoped decode reads": {
				input: "name: api\ntier: {a: b}\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.DecodeAt[tier](ctx, paths.Current().Child("tier"))

					return err
				},
				want: "2:1: $.tier: expected string, got mapping",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				assert.True(t, niceyaml.IsInvalid(err))

				requireNamedPath(t, yamltest.RequireSourceError(t, err), tc.want)
			})
		}
	})

	t.Run("comes back with no location", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
			source []niceyaml.SourceOption
		}{
			// The fields of the value mirror nothing, and the mapping the
			// decode reads is the whole of what the caller asked for.
			"mapping the decode reads": {
				input:  "a: 1\nb: bad\n",
				decode: decodeAs[opaqueValue](),
				want:   "unmarshaler rejected the value",
			},
			"value that adds text at the node the decode reads": {
				input:  midTier,
				decode: decodeAs[prefixing](),
				want:   `server: unknown tier "mid"`,
			},
			// The second decode reads a new value, which is not locked.
			"unmarshaler that reads the value the caller filled": {
				input: "a: x\nb: y\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					v := struct {
						A lockable `yaml:"a"`
						B lockable `yaml:"b"`
					}{B: lockable{Locked: true}}

					return dd.DecodeInto(ctx, &v)
				},
				want: "unmarshaler rejected the value",
			},
			// The second decode calls the unmarshaler again, and the panic
			// of that call reproduces nothing and adds no problem.
			"unmarshaler that panics in the second decode": {
				input: "a: 1\nv: x\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						A int         `yaml:"a"`
						V rejectsOnce `yaml:"v"`
					}](context.WithValue(ctx, panicCallsKey{}, new(atomic.Int32)))

					return err
				},
				want: "unmarshaler rejected the value",
			},
			// The decoder shows no type a function registered for the
			// whole program decodes.
			"type a registered function decodes": {
				input: "name: api\nvalue: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					yaml.RegisterCustomUnmarshaler(func(*registeredDecoded, []byte) error { return errUnmarshal })

					_, err := dd.Decode[struct {
						Name  string            `yaml:"name"`
						Value registeredDecoded `yaml:"value"`
					}](ctx)

					return err
				},
				want: "unmarshaler rejected the value",
			},
			// No path resolves through an alias to a reference document.
			"value an alias reads from a reference document": {
				input: "timeout: *limit\n",
				source: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("limit: &limit soon\n")),
				},
				decode: decodeAs[tierServer](),
				want:   `time: invalid duration "soon"`,
			},
			// Every decode of the node fails the same way, whatever the
			// target, so no value of the target reported the error.
			"error of the decoder itself": {
				input: "timeout: soon\n",
				source: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("limit: [\n")),
				},
				decode: decodeAs[tierServer](),
				want: stringtest.LinesLF(
					"[1:8] sequence end token ']' not found",
					">  1 | limit: [",
					"              ^",
				),
			},
			// The walk decodes nothing once the context has ended.
			"context an unmarshaler ends": {
				input: "- low\n- mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()

					_, err := dd.Decode[[]canceling](context.WithValue(ctx, cancelKey{}, cancel))

					return err
				},
				want: "unmarshaler rejected the value",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.source...)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				srcErr := yamltest.RequireSourceError(t, err)

				_, ok := srcErr.Path()
				assert.False(t, ok, "the error took a path")

				_, ok = srcErr.Range()
				assert.False(t, ok, "the error took a location")
			})
		}
	})

	t.Run("matches the decode sentinel", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
		}{
			"field": {
				input:  "name: api\ntimeout: 5 minutes\n",
				decode: decodeAs[tierServer](),
				want:   `2:10: $.timeout: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"scalar the decode reads": {
				input:  "5 minutes\n",
				decode: decodeAs[time.Duration](),
				want:   `1:1: $: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"map key": {
				input:  "5 minutes: a\n",
				decode: decodeAs[map[time.Duration]string](),
				want:   `1:1: $.'5 minutes'~: time: unknown unit " minutes" in duration "5 minutes"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}

		// The error of an unmarshaler is the value's own, so it keeps what
		// it matched beside the sentinel.
		dd := yamltest.FirstDocument(t, midTier)

		_, err := dd.Decode[tierServer](t.Context())
		require.EqualError(t, err, `2:7: $.tier: unknown tier "mid"`)
		require.ErrorIs(t, err, errUnknownTier)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	// An unmarshaler holds no Node, so the path it writes reads from its
	// own value, as the path of a niceyaml.SelfValidator does.
	t.Run("puts the path an unmarshaler writes under its value", func(t *testing.T) {
		t.Parallel()

		// Returns a decode of a document whose items report err.
		reported := func(err error) func(context.Context, *niceyaml.Node) error {
			return func(ctx context.Context, dd *niceyaml.Node) error {
				_, derr := dd.Decode[struct {
					Name  string      `yaml:"name"`
					Items []reporting `yaml:"items"`
				}](context.WithValue(ctx, reportKey{}, err))

				return derr
			}
		}

		items := stringtest.LinesLF(
			"name: api",
			"items:",
			"  - from: 9",
			"    to: 3",
		)
		twoSpans := stringtest.LinesLF(
			"spans:",
			"  - {from: 1, to: 5}",
			"  - from: 9",
			"    to: 3",
		)
		mainSpan := stringtest.LinesLF("main:", "  span: {from: 9, to: 3}")

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			is     error
			input  string
			want   string
		}{
			"method that takes a decode function": {
				input: twoSpans,
				decode: decodeAs[struct {
					Spans []span `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "4:9: $.spans[1].to: to is below from",
			},
			"method that takes the node": {
				input: oneSpan,
				decode: decodeAs[struct {
					Spans []spanNode `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "3:9: $.spans[0].to: to is below from",
			},
			"method that takes bytes": {
				input: oneSpan,
				decode: decodeAs[struct {
					Spans []spanBytes `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "3:9: $.spans[0].to: to is below from",
			},
			"function of an option": {
				input: "name: api\nvalue: {from: 9, to: 3}\n",
				decode: decodeAs[struct {
					Name  string        `yaml:"name"`
					Value optionDecoded `yaml:"value"`
				}](niceyaml.WithCustomUnmarshaler(
					func(context.Context, *optionDecoded, func(any) error) error { return spanError() },
				)),
				is:   errSpan,
				want: "2:22: $.value.to: to is below from",
			},
			"map value": {
				input:  stringtest.LinesLF("a: {from: 1, to: 2}", "b: {from: 9, to: 3}"),
				decode: decodeAs[map[string]span](),
				is:     errSpan,
				want:   "2:18: $.b.to: to is below from",
			},
			"pointer field": {
				input: stringtest.LinesLF(
					"span:",
					"  from: 9",
					"  to: 3",
				),
				decode: decodeAs[struct {
					Span *span `yaml:"span"`
				}](),
				is:   errSpan,
				want: "3:7: $.span.to: to is below from",
			},
			// The path goes through the alias to the line that holds the
			// value.
			"value an alias holds": {
				input: stringtest.LinesLF("base: &base {from: 9, to: 3}", "spans: [*base]"),
				decode: decodeAs[struct {
					Spans []span `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "1:27: $.spans[0].to: to is below from",
			},
			"field a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base {to: 3}",
					"spans:",
					"  - <<: *base",
					"    from: 9",
				),
				decode: decodeAs[struct {
					Spans []span `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "1:18: $.spans[0].to: to is below from",
			},
			"value below a scoped node": {
				input: twoSpans,
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.DecodeAt[[]span](ctx, paths.Current().Child("spans"))

					return err
				},
				is:   errSpan,
				want: "4:9: $.spans[1].to: to is below from",
			},
			// No value below the node reports the error, so the path reads
			// from the node, which is the value.
			"mapping the decode reads": {
				input:  "from: 9\nto: 3\n",
				decode: decodeAs[span](),
				is:     errSpan,
				want:   "2:5: $.to: to is below from",
			},
			"mapping a scoped decode reads": {
				input: twoSpans,
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.DecodeAt[span](ctx, paths.Current().Child("spans").Index(1))

					return err
				},
				is:   errSpan,
				want: "4:9: $.spans[1].to: to is below from",
			},
			// The span wrote the path, so the path goes under the span and
			// not under the value that handed its error on.
			"value that returns the error of a field": {
				input: stringtest.LinesLF(
					"mirrors:",
					"  - name: a",
					"    span: {from: 9, to: 3}",
				),
				decode: decodeAs[struct {
					Mirrors []spanMirror `yaml:"mirrors"`
				}](),
				is:   errSpan,
				want: "3:25: $.mirrors[0].span.to: to is below from",
			},
			"value that adds text to the error of a field": {
				input: mainSpan,
				decode: decodeAs[struct {
					Main spanWrapper `yaml:"main"`
				}](),
				is:   errSpan,
				want: "2:23: $.main.span.to: wrapper: to is below from",
			},
			// The value wrote the path of its field, so the path goes
			// under the value once, and not under the field too.
			"value that rebases the error of a field": {
				input: mainSpan,
				decode: decodeAs[struct {
					Main spanRebaser `yaml:"main"`
				}](),
				is:   errSpan,
				want: "2:23: $.main.span.to: to is below from",
			},
			"value that names a field in the error of that field": {
				input: stringtest.LinesLF("main:", "  tier: mid"),
				decode: decodeAs[struct {
					Main tierNamer `yaml:"main"`
				}](),
				is:   errUnknownTier,
				want: `2:9: $.main.tier: unknown tier "mid"`,
			},
			"path of the value itself": {
				input:  items,
				decode: reported(niceyaml.Invalid(errUnmarshal, niceyaml.AtPath(paths.Current()))),
				is:     errUnmarshal,
				want:   "3:3: $.items[0]: unmarshaler rejected the value",
			},
			"error with no location": {
				input:  items,
				decode: reported(niceyaml.Invalid(errUnmarshal)),
				is:     errUnmarshal,
				want:   "3:3: $.items[0]: unmarshaler rejected the value",
			},
			"rebased error with no location": {
				input:  items,
				decode: reported(niceyaml.Rebase(errUnmarshal, paths.Current().Child("to"))),
				is:     errUnmarshal,
				want:   "4:9: $.items[0].to: unmarshaler rejected the value",
			},
			// A path from the root reads from no value, so it stays.
			"path from the root": {
				input:  items,
				decode: reported(niceyaml.Invalid(errUnmarshal, niceyaml.AtPath(paths.Doc().Child("name")))),
				is:     errUnmarshal,
				want:   "1:7: $.name: unmarshaler rejected the value",
			},
			// Each problem of a summary goes under the value on its own.
			"summary": {
				input: items,
				decode: reported(niceyaml.NewSummary(
					"2 problems in the span",
					niceyaml.NewError("from is odd", niceyaml.AtPath(paths.Current().Child("from"))),
					niceyaml.NewError("to is odd", niceyaml.AtPath(paths.Current().Child("to"))),
				)),
				want: stringtest.JoinLF(
					"2 problems in the span",
					"3:11: $.items[0].from: from is odd",
					"4:9: $.items[0].to: to is odd",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				if tc.is != nil {
					require.ErrorIs(t, err, tc.is)
				}

				srcErr := yamltest.RequireSourceError(t, err)

				// A summary points at no value, so it carries no path.
				if len(yamltest.ProblemsOf(srcErr)) > 0 {
					_, ok := srcErr.Path()
					assert.False(t, ok)

					return
				}

				requireNamedPath(t, srcErr, tc.want)
			})
		}
	})

	// An error that heads several problems names no place itself. The
	// places of its problems tell the error a value rebased from the
	// error of its field, so each path goes under the value once.
	t.Run("puts the problems a value rebased under that value once", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			report error
			want   string
		}{
			"summary": {
				report: niceyaml.NewSummary(
					"2 problems in the span",
					niceyaml.NewError("from is odd", niceyaml.AtPath(paths.Current().Child("from"))),
					niceyaml.NewError("to is odd", niceyaml.AtPath(paths.Current().Child("to"))),
				),
				want: stringtest.JoinLF(
					"2 problems in the span",
					"3:11: $.main.item.from: from is odd",
					"4:9: $.main.item.to: to is odd",
				),
			},
			"join": {
				report: errors.Join(errors.New("from is odd"), errors.New("to is odd")),
				want: stringtest.JoinLF(
					"2:3: $.main.item: from is odd",
					"2:3: $.main.item: to is odd",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, stringtest.LinesLF(
					"main:",
					"  item:",
					"    from: 9",
					"    to: 3",
				))

				_, err := dd.Decode[struct {
					Main rebasing `yaml:"main"`
				}](context.WithValue(t.Context(), reportKey{}, tc.report))
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	// The detail explains the error, so it points at nothing itself, and
	// its path goes under the value all the same.
	t.Run("puts the path of a detail under the value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, oneSpan)
		report := niceyaml.NewError("span is odd", niceyaml.WithDetails(
			niceyaml.NewError("to set here", niceyaml.AtPath(paths.Current().Child("to"))),
		))

		_, err := dd.Decode[struct {
			Spans []reporting `yaml:"spans"`
		}](context.WithValue(t.Context(), reportKey{}, error(report)))
		require.EqualError(t, err, "2:3: $.spans[0]: span is odd")

		srcErr := yamltest.RequireSourceError(t, err)
		require.Len(t, yamltest.DetailsOf(srcErr), 1)
		assert.EqualError(t, yamltest.DetailsOf(srcErr)[0].Bound, "3:9: $.spans[0].to: to set here")
	})

	t.Run("keeps the location the error carries", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: api\nvalue: [a, b]\n")

		_, err := dd.Decode[struct {
			Name  string     `yaml:"name"`
			Value positioned `yaml:"value"`
		}](t.Context())
		require.EqualError(t, err, "2:8: unmarshaler rejected the value")
		require.ErrorIs(t, err, errUnmarshal)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)

		_, ok := srcErr.Path()
		assert.False(t, ok, "the error took a path")
	})

	t.Run("leaves the decoded value alone", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: x",
			"b: y",
			"c: z",
		))

		// The second decode fills values of its own, so the value the
		// caller holds keeps what the first decode set.
		var v struct {
			A lockable `yaml:"a"`
			B tier     `yaml:"b"`
			C lockable `yaml:"c"`
		}

		err := dd.DecodeInto(t.Context(), &v)
		require.EqualError(t, err, `2:4: $.b: unknown tier "y"`)
		assert.Equal(t, lockable{Text: "x"}, v.A)
		assert.Equal(t, lockable{Text: "z"}, v.C)
	})
}
