package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
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

// The error tier reports for text it does not know.
var errUnknownTier = errors.New("unknown tier")

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
// under yaml.UseJSONUnmarshaler.
type jsonDecoded struct {
	V string `yaml:"v"`
}

func (*jsonDecoded) UnmarshalJSON([]byte) error {
	return errUnmarshal
}

// jsonFielded has an UnmarshalJSON method the decoder calls only under
// yaml.UseJSONUnmarshaler. Without that option the decoder reads its
// field.
type jsonFielded struct {
	Tier tier `yaml:"tier"`
}

func (*jsonFielded) UnmarshalJSON([]byte) error {
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

// positioned decodes itself from its node and reports an error at the
// position of that node.
type positioned struct{}

func (*positioned) UnmarshalYAML(node ast.Node) error {
	return niceyaml.Invalid(errUnmarshal, niceyaml.AtPosition(position.NewFromToken(node.GetToken())))
}

// optionDecoded has no method of its own. A yaml.CustomUnmarshaler
// option decodes it.
type optionDecoded struct{}

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

func TestDocument_Decode_Rejection(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		path  string
		opts  []niceyaml.DecodeOption
	}{
		"scalar in an element": {
			input: "servers:\n  - host: h\n    port: abc\n",
			want:  "3:11: $.servers[0].port: expected integer, got string",
			path:  "$.servers[0].port",
		},
		"block mapping for an integer": {
			input: "top:\n  a: 1\n  b: 2\n",
			want:  "2:3: $.top: expected integer, got mapping",
			path:  "$.top",
		},
		"block mapping for a string in a map": {
			input: "labels:\n  a:\n    b: 1\n",
			want:  "3:5: $.labels.a: expected string, got mapping",
			path:  "$.labels.a",
		},
		"block sequence for an integer": {
			input: "top:\n  - 1\n  - 2\n",
			want:  "2:5: $.top: expected integer, got sequence",
			path:  "$.top",
		},
		"flow sequence for a string": {
			input: "name: [a, b]\n",
			want:  "1:8: $.name: expected string, got sequence",
			path:  "$.name",
		},
		"flow mapping for an integer": {
			input: "top: {a: 1}\n",
			want:  "1:7: $.top: expected integer, got mapping",
			path:  "$.top",
		},
		"empty flow sequence for an integer": {
			input: "top: []\n",
			want:  "1:6: $.top: expected integer, got sequence",
			path:  "$.top",
		},
		"string for a sequence": {
			input: "servers: abc\n",
			want:  "1:10: $.servers: expected sequence, got string",
			path:  "$.servers",
		},
		"string for a mapping": {
			input: "one: abc\n",
			want:  "1:6: $.one: expected mapping, got string",
			path:  "$.one",
		},
		"block sequence for a mapping": {
			input: "one:\n  - a\n",
			want:  "2:5: $.one: expected mapping, got sequence",
			path:  "$.one",
		},
		"sequence element for a mapping": {
			input: "servers:\n  - - a\n",
			want:  "2:7: $.servers[0]: expected mapping, got sequence",
			path:  "$.servers[0]",
		},
		"integer for a sequence": {
			input: "servers: 5\n",
			want:  "1:10: $.servers: expected sequence, got integer",
			path:  "$.servers",
		},
		"boolean for a mapping": {
			input: "one: true\n",
			want:  "1:6: $.one: expected mapping, got boolean",
			path:  "$.one",
		},
		"block scalar for a mapping": {
			input: "one: |\n  abc\n",
			want:  "1:6: $.one: expected mapping, got string",
			path:  "$.one",
		},
		"anchored scalar": {
			input: "top: &t abc\n",
			want:  "1:9: $.top: expected integer, got string",
			path:  "$.top",
		},
		"tagged scalar": {
			input: "top: !!str abc\n",
			want:  "1:12: $.top: expected integer, got string",
			path:  "$.top",
		},
		"value an alias reads": {
			input: "defs:\n  p: &p abc\nservers:\n  - port: *p\n",
			want:  "2:9: $.defs.p: expected integer, got string",
			path:  "$.defs.p",
		},
		"value a merge key brings in": {
			input: "defs: &d\n  port: abc\nservers:\n  - <<: *d\n    host: h\n",
			want:  "2:9: $.defs.port: expected integer, got string",
			path:  "$.defs.port",
		},
		"map key": {
			input: "counts:\n  x: 1\n",
			want:  "2:3: $.counts.x~: expected integer, got string",
			path:  "$.counts.x~",
		},
		"explicit map key": {
			input: "counts:\n  ? x\n  : 1\n",
			want:  "2:5: $.counts.x~: expected integer, got string",
			path:  "$.counts.x~",
		},
		"map value": {
			input: "counts:\n  1: x\n",
			want:  "2:6: $.counts.1: expected integer, got string",
			path:  "$.counts.1",
		},
		"key a path quotes": {
			input: "labels:\n  \"a.b\": [1]\n",
			want:  "2:11: $.labels.'a.b': expected string, got sequence",
			path:  "$.labels.'a.b'",
		},
		"overflow of a signed integer": {
			input: "servers:\n  - small: 300\n",
			want:  "2:12: $.servers[0].small: expected integer from -128 to 127, got 300",
			path:  "$.servers[0].small",
		},
		"negative for an unsigned integer": {
			input: "servers:\n  - wide: -1\n",
			want:  "2:11: $.servers[0].wide: expected integer from 0 to 65535, got -1",
			path:  "$.servers[0].wide",
		},
		"overflow of an unsigned integer": {
			input: "one: {wide: 70000}\n",
			want:  "1:13: $.one.wide: expected integer from 0 to 65535, got 70000",
			path:  "$.one.wide",
		},
		"float past an integer": {
			input: "one: {small: 1e10}\n",
			want:  "1:14: $.one.small: expected integer from -128 to 127, got 1e10",
			path:  "$.one.small",
		},
		"boolean for an integer": {
			input: "top: true\n",
			want:  "1:6: $.top: expected integer, got boolean",
			path:  "$.top",
		},
		"integer for a boolean": {
			input: "on: 5\n",
			want:  "1:5: $.on: expected boolean, got integer",
			path:  "$.on",
		},
		"mapping for a float": {
			input: "ratio: {a: 1}\n",
			want:  "1:9: $.ratio: expected float, got mapping",
			path:  "$.ratio",
		},
		"sequence for a timestamp": {
			input: "when: [1]\n",
			want:  "1:8: $.when: expected timestamp, got sequence",
			path:  "$.when",
		},
		"sequence for a duration": {
			input: "wait: [1]\n",
			want:  "1:8: $.wait: expected duration, got sequence",
			path:  "$.wait",
		},
		"tagged null for an integer": {
			input: "top: !!null x\n",
			want:  "1:13: $.top: expected integer, got null",
			path:  "$.top",
		},
		"binary for an integer": {
			input: "top: !!binary YWJj\n",
			want:  "1:15: $.top: expected integer, got binary",
			path:  "$.top",
		},
		"timestamp for an integer": {
			input: "top: !!timestamp 2020-01-01\n",
			want:  "1:18: $.top: expected integer, got timestamp",
			path:  "$.top",
		},
		"ordered mapping for an integer": {
			input: "top: {a: 1}\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithYAMLDecodeOptions(yaml.UseOrderedMap())},
			want:  "1:7: $.top: expected integer, got mapping",
			path:  "$.top",
		},
		"unknown field": {
			input: "name: x\nfoo: 1\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  "2:1: $.foo~: unknown field \"foo\"",
			path:  "$.foo~",
		},
		"unknown field in an element": {
			input: "servers:\n  - zzz: 1\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  "2:5: $.servers[0].zzz~: unknown field \"zzz\"",
			path:  "$.servers[0].zzz~",
		},
		"unknown field a merge key brings in": {
			input: "labels: &d\n  zzz: 1\none:\n  <<: *d\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  "2:3: $.labels.zzz~: unknown field \"zzz\"",
			path:  "$.labels.zzz~",
		},
		"alias with no anchor": {
			input: "top: *nope\n",
			want:  "1:6: $.top: could not find alias \"nope\"",
			path:  "$.top",
		},
		"value under renamed anchors": {
			input: "a: &x 1\nb: &x 2\ntop: abc\n",
			want:  "3:6: $.top: expected integer, got string",
			path:  "$.top",
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

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr)
			require.NoError(t, srcErr.Unresolved())

			path, ok := srcErr.Path()
			require.True(t, ok, "the rejection carries no path")
			assert.Equal(t, tc.path, path.String())

			// The message names no Go type or field.
			for _, word := range []string{"unmarshal", "Go ", "struct", "interface"} {
				assert.NotContains(t, srcErr.Message(), word)
			}

			// The rejection binds where an error at its path binds, as one
			// from a validator does.
			var atPath *niceyaml.SourceError

			require.ErrorAs(t, dd.Bind(niceyaml.NewError("at the path", niceyaml.AtPath(path))), &atPath)
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

			if strings.HasSuffix(tc.path, "~") {
				assert.Equal(t, tc.path, under.String())

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

	merge := "defs: &d\n  a: 1\nv:\n  <<: *d\n  b: 1\n"

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error
		input  string
		want   string
	}{
		"channel": {
			decode: decodeInto[struct {
				V chan int `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		"pointer to a channel": {
			decode: decodeInto[struct {
				V *chan int `yaml:"v"`
			}](),
			input: "v: {a: 1}\n",
			want:  "1:5: $.v: expected no value, got mapping",
		},
		"function": {
			decode: decodeInto[struct {
				V func() `yaml:"v"`
			}](),
			input: "v: abc\n",
			want:  "1:4: $.v: expected no value, got string",
		},
		"complex number": {
			decode: decodeInto[struct {
				V complex128 `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		"element of an array of complex numbers": {
			decode: decodeInto[struct {
				V [2]complex64 `yaml:"v"`
			}](),
			input: "v: [1.5]\n",
			want:  "1:5: $.v[0]: expected no value, got float",
		},
		"value of a map of functions": {
			decode: decodeInto[struct {
				V map[string]func() `yaml:"v"`
			}](),
			input: "v: {a: true}\n",
			want:  "1:8: $.v.a: expected no value, got boolean",
		},
		"unsafe pointer": {
			decode: decodeInto[struct {
				V unsafe.Pointer `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		// The decoder fills one pointer around a value and no more.
		"pointer to a pointer": {
			decode: decodeInto[struct {
				V **int `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected integer, got integer of another type",
		},
		"anchor read into an interface": {
			decode: decodeInto[struct {
				Defs any     `yaml:"defs"`
				V    aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "2:3: $.defs: expected mapping, got value of another type",
		},
		"anchor read into another struct": {
			decode: decodeInto[struct {
				Defs *other  `yaml:"defs"`
				V    aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "2:3: $.defs: expected mapping, got mapping of another type",
		},
		"anchor read into a map": {
			decode: decodeInto[struct {
				Defs map[string]int `yaml:"defs"`
				V    aliased        `yaml:"v"`
			}](),
			input: merge,
			want:  "2:3: $.defs: expected mapping, got mapping of another type",
		},
		"anchor read into no field": {
			decode: decodeInto[struct {
				V aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "2:3: $.defs: expected mapping, got mapping of another type",
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

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr)

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

	// The message reads in YAML terms, and the error the decoder returned
	// stays in the chain with its Go types.
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
		path  string
	}{
		// The path starts at `$`, so the scope of the Node does not go
		// in front of it a second time.
		"value inside the scope": {
			scope: "$.servers[0]",
			want:  "s.yaml:5:11: $.servers[0].port: expected integer, got string",
			path:  "$.servers[0].port",
		},
		// The merge key brings the value in from outside the scope,
		// and the path names the place where the anchor defines it.
		"value a merge key brings in from outside the scope": {
			scope: "$.servers[1]",
			want:  "s.yaml:2:9: $.defs.port: expected integer, got string",
			path:  "$.defs.port",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scoped := yamltest.At(t, doc, paths.MustParse(tc.scope))

			_, err := scoped.Decode[rejectionServer](t.Context())
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr)

			path, ok := srcErr.Path()
			require.True(t, ok)
			assert.Equal(t, tc.path, path.String())

			// The error stays bound to the Node that decoded, and its
			// path resolves from the root of the document.
			assert.Same(t, scoped, srcErr.Node())
			assert.Same(t, doc, srcErr.Document())

			ranges, err := srcErr.Document().Ranges(path)
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
		doc := yamltest.FirstDocument(t, "name: 1\nname: y\nname: 3\n", niceyaml.WithAllowDuplicateKeys(true))

		_, err := doc.Decode[map[string]int](t.Context())
		require.EqualError(t, err, "2:7: $.name: expected integer, got string")
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)
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

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		_, ok := srcErr.Path()
		assert.False(t, ok)

		pos, ok := srcErr.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 1), pos)
	})
}

func TestDocument_Decode_UnmarshalerError(t *testing.T) {
	t.Parallel()

	t.Run("binds at the value that reported it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
			path   string
		}{
			"duration field": {
				input: "name: api\ntimeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `2:10: $.timeout: time: invalid duration "soon"`,
				path: "$.timeout",
			},
			"text unmarshaler in a sequence of structs": {
				input: "- name: a\n  tier: low\n- name: b\n  tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[[]tierServer](ctx)

					return err
				},
				want: `4:9: $[1].tier: unknown tier "mid"`,
				path: "$[1].tier",
			},
			"pointer element": {
				input: "tiers: [low, mid]\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Tiers []*tier `yaml:"tiers"`
					}](ctx)

					return err
				},
				want: `1:14: $.tiers[1]: unknown tier "mid"`,
				path: "$.tiers[1]",
			},
			"map key": {
				input: "hosts:\n  10.0.0.1: a\n  nope: b\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Hosts map[netip.Addr]string `yaml:"hosts"`
					}](ctx)

					return err
				},
				want: `3:3: $.hosts.nope~: ParseAddr("nope"): unable to parse IP`,
				path: "$.hosts.nope~",
			},
			"map value": {
				input: "a: low\nb: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[string]tier](ctx)

					return err
				},
				want: `2:4: $.b: unknown tier "mid"`,
				path: "$.b",
			},
			// The path through a merge key resolves to the entry of the
			// mapping the key brings in.
			"map value a merge key brings in": {
				input: "base: &base\n  timeout: soon\nservers:\n  api:\n    <<: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Servers map[string]tierServer `yaml:"servers"`
					}](ctx)

					return err
				},
				want: `2:12: $.servers.api.timeout: time: invalid duration "soon"`,
				path: "$.servers.api.timeout",
			},
			"map entry a merge key brings in": {
				input: "base: &base\n  b: mid\ntiers:\n  a: low\n  <<: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Tiers map[string]tier `yaml:"tiers"`
					}](ctx)

					return err
				},
				want: `2:6: $.tiers.b: unknown tier "mid"`,
				path: "$.tiers.b",
			},
			// The path of a value an alias holds points at the alias.
			"alias": {
				input: "label: &label mid\ntiers: [low, *label]\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Label string `yaml:"label"`
						Tiers []tier `yaml:"tiers"`
					}](ctx)

					return err
				},
				want: `2:14: $.tiers[1]: unknown tier "mid"`,
				path: "$.tiers[1]",
			},
			"inline struct": {
				input: "id: 1\nname: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Server tierServer `yaml:",inline"`
						ID     int        `yaml:"id"`
					}](ctx)

					return err
				},
				want: `3:7: $.tier: unknown tier "mid"`,
				path: "$.tier",
			},
			"inline map": {
				input: "id: low\nextra: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Tiers map[string]tier `yaml:",inline"`
						ID    string          `yaml:"id"`
					}](ctx)

					return err
				},
				want: `2:8: $.extra: unknown tier "mid"`,
				path: "$.extra",
			},
			// The decoder reads the fields of a struct in the order the
			// struct declares them, so the first field that fails reports
			// the error, wherever the document puts it.
			"first field in struct order": {
				input: "tier: mid\ntimeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `2:10: $.timeout: time: invalid duration "soon"`,
				path: "$.timeout",
			},
			"value that decodes through a second type": {
				input: "name: prod\nservers:\n  - name: a\n    tier: low\n  - name: b\n    tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[mirrored](ctx)

					return err
				},
				want: `6:11: $.servers[1].tier: unknown tier "mid"`,
				path: "$.servers[1].tier",
			},
			"field that decodes through a second type": {
				input: "prod:\n  servers:\n    - timeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Prod mirrored `yaml:"prod"`
					}](ctx)

					return err
				},
				want: `3:16: $.prod.servers[0].timeout: time: invalid duration "soon"`,
				path: "$.prod.servers[0].timeout",
			},
			// No field below the value reports the message with the text
			// the value put in front, so the error points at the value.
			"value that adds text to the error of a field": {
				input: "main:\n  name: api\n  tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Main prefixing `yaml:"main"`
					}](ctx)

					return err
				},
				want: `2:3: $.main: server: unknown tier "mid"`,
				path: "$.main",
			},
			"value whose fields mirror nothing": {
				input: "name: api\nvalue:\n  a: 1\n  b: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string      `yaml:"name"`
						Value opaqueValue `yaml:"value"`
					}](ctx)

					return err
				},
				want: `3:3: $.value: unmarshaler rejected the value`,
				path: "$.value",
			},
			"json unmarshaler under its option": {
				input: "name: api\nvalue:\n  v: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string      `yaml:"name"`
						Value jsonDecoded `yaml:"value"`
					}](ctx, niceyaml.WithYAMLDecodeOptions(yaml.UseJSONUnmarshaler()))

					return err
				},
				want: `3:3: $.value: unmarshaler rejected the value`,
				path: "$.value",
			},
			// The decoder reads the value field by field, so the field
			// reported the error.
			"json unmarshaler without its option": {
				input: "name: api\nvalue:\n  tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string      `yaml:"name"`
						Value jsonFielded `yaml:"value"`
					}](ctx)

					return err
				},
				want: `3:9: $.value.tier: unknown tier "mid"`,
				path: "$.value.tier",
			},
			// The scalar the decode reads is the value, so the error
			// points at the node itself, which the path names from the
			// root of the document.
			"scalar the decode reads": {
				input: "name: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					node, err := dd.At(paths.Current().Child("tier"))
					if err != nil {
						return err //nolint:wrapcheck // The test inspects the error as it is.
					}

					_, err = node.Decode[tier](ctx)

					return err
				},
				want: `2:7: $.tier: unknown tier "mid"`,
				path: "$.tier",
			},
			"value below a scoped node": {
				input: "servers:\n  - name: a\n    tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					node, err := dd.At(paths.Current().Child("servers").Index(0))
					if err != nil {
						return err //nolint:wrapcheck // The test inspects the error as it is.
					}

					_, err = node.Decode[tierServer](ctx)

					return err
				},
				want: `3:11: $.servers[0].tier: unknown tier "mid"`,
				path: "$.servers[0].tier",
			},
			"DecodeInto": {
				input: "name: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v tierServer

					return dd.DecodeInto(ctx, &v)
				},
				want: `2:7: $.tier: unknown tier "mid"`,
				path: "$.tier",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				path, ok := srcErr.Path()
				require.True(t, ok, "the error carries no path")
				assert.Equal(t, tc.path, path.String())

				_, ok = srcErr.Range()
				assert.True(t, ok, "the error carries no location")
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
				input: "a: 1\nb: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[opaqueValue](ctx)

					return err
				},
				want: "unmarshaler rejected the value",
			},
			"value that adds text at the node the decode reads": {
				input: "name: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[prefixing](ctx)

					return err
				},
				want: `server: unknown tier "mid"`,
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
			// The decoder shows no type an option decodes.
			"type an option decodes": {
				input: "name: api\nvalue: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string        `yaml:"name"`
						Value optionDecoded `yaml:"value"`
					}](ctx, niceyaml.WithYAMLDecodeOptions(yaml.CustomUnmarshaler(
						func(*optionDecoded, []byte) error { return errUnmarshal },
					)))

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
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `time: invalid duration "soon"`,
			},
			// Every decode of the node fails the same way, whatever the
			// target, so no value of the target reported the error.
			"error of the decoder itself": {
				input: "timeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx, niceyaml.WithYAMLDecodeOptions(
						yaml.ReferenceFiles("testdata/no-such-reference.yaml"),
					))

					return err
				},
				want: "open testdata/no-such-reference.yaml: no such file or directory",
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

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

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
				input: "name: api\ntimeout: 5 minutes\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `2:10: $.timeout: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"scalar the decode reads": {
				input: "5 minutes\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[time.Duration](ctx)

					return err
				},
				want: `1:1: $: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"map key": {
				input: "5 minutes: a\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[time.Duration]string](ctx)

					return err
				},
				want: `1:1: $.'5 minutes'~: time: unknown unit " minutes" in duration "5 minutes"`,
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
		dd := yamltest.FirstDocument(t, "name: api\ntier: mid\n")

		_, err := dd.Decode[tierServer](t.Context())
		require.EqualError(t, err, `2:7: $.tier: unknown tier "mid"`)
		require.ErrorIs(t, err, errUnknownTier)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
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

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		_, ok := srcErr.Path()
		assert.False(t, ok, "the error took a path")
	})

	t.Run("leaves the decoded value alone", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: x\nb: y\nc: z\n")

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
