package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// strictInner is a struct with two fields, for the decodes that reject
// unknown fields.
type strictInner struct {
	A int `yaml:"a"`
	B int `yaml:"b"`
}

// StrictDeep is an inline field of [StrictNested]. The decoder sets an
// embedded field only when its type is exported.
type StrictDeep struct {
	D int `yaml:"d"`
}

// StrictNested is an inline struct that holds another inline struct.
type StrictNested struct {
	C          int `yaml:"c"`
	StrictDeep `yaml:",inline"`
}

// strictSelf decodes itself through a second type with the same fields,
// so the decoder checks those fields, at the struct and below it.
type strictSelf struct {
	N []strictInner `yaml:"n"`
	X int           `yaml:"x"`
}

func (s *strictSelf) UnmarshalYAML(unmarshal func(any) error) error {
	type plain strictSelf

	return unmarshal((*plain)(s))
}

// strictText decodes itself from the text of its node with a decoder of
// its own, which skips unknown fields, so the decoder of the decode
// checks none of its fields.
type strictText struct {
	N strictInner `yaml:"n"`
	X int         `yaml:"x"`
}

func (s *strictText) UnmarshalYAML(data []byte) error {
	type plain strictText

	return yaml.Unmarshal(data, (*plain)(s)) //nolint:wrapcheck // The decode reports the error as it is.
}

// StrictBase is the type of an anchor that [strictAliased] reads through
// its inline field.
type StrictBase struct {
	A int `yaml:"a"`
}

// strictAliased holds an inline field that reads the anchor a `<<` key
// names. With the omitempty option beside it, the decoder leaves out the
// entries the `<<` keys bring in.
type strictAliased struct {
	*StrictBase `yaml:",omitempty,inline,alias"`

	In strictInner `yaml:"in"`
	B  int         `yaml:"b"`
}

// decodeInto returns a decode of a node into a new T, for a table of
// decodes into different types.
func decodeInto[T any]() func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error {
	return func(ctx context.Context, n *niceyaml.Node, opts ...niceyaml.DecodeOption) error {
		var v T

		return n.DecodeInto(ctx, &v, opts...)
	}
}

// rejectionRows returns the text of each problem [niceyaml.ErrorTree]
// lists for err, one per line, with the position and the path in front
// of each message.
func rejectionRows(err error) string {
	var rows []string

	for node := range niceyaml.NewErrorTree(err).Problems() {
		rows = append(rows, node.Text)
	}

	return strings.Join(rows, "\n")
}

func TestDocument_Decode_UnknownFields(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		decode     func(context.Context, *niceyaml.Node, ...niceyaml.DecodeOption) error
		input      string
		scope      string
		want       string
		sourceOpts []niceyaml.SourceOption
		opts       []niceyaml.DecodeOption
	}{
		// The decoder reports one of the three, and not always the same
		// one. The rows come in the order of the source.
		"several in one mapping": {
			decode: decodeInto[struct {
				Name string `yaml:"name"`
			}](),
			input: "baz: 3\nname: x\nfoo: 1\nbar: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`1:1: $.baz~: unknown field "baz"`,
				`3:1: $.foo~: unknown field "foo"`,
				`4:1: $.bar~: unknown field "bar"`,
			),
		},
		"several levels": {
			decode: decodeInto[struct {
				Name    string        `yaml:"name"`
				One     strictInner   `yaml:"one"`
				Servers []strictInner `yaml:"servers"`
			}](),
			input: "top: 1\none:\n  a: 1\n  x: 2\nservers:\n  - b: 1\n    y: 2\n  - z: 3\nname: n\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`1:1: $.top~: unknown field "top"`,
				`4:3: $.one.x~: unknown field "x"`,
				`7:5: $.servers[0].y~: unknown field "y"`,
				`8:5: $.servers[1].z~: unknown field "z"`,
			),
		},
		// The fields of an inline struct are fields of the struct that
		// holds it, however deep the inline structs nest.
		"inline fields": {
			decode: decodeInto[struct {
				In           *strictInner `yaml:",inline"`
				Name         string       `yaml:"name"`
				StrictNested `yaml:",inline"`
			}](),
			input: "name: x\nc: 1\nd: 2\na: 3\nzzz: 4\nyyy: 5\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`5:1: $.zzz~: unknown field "zzz"`,
				`6:1: $.yyy~: unknown field "yyy"`,
			),
		},
		// A field reads the key of its yaml tag, its json tag, or its
		// lowercased name, and no other spelling. A field the decoder
		// skips reads no key.
		"field names": {
			decode: decodeInto[struct {
				Tagged   string `json:"nm"`
				Upper    string
				Dashed   string `yaml:"-"`
				hidden   string
				Embedded strictInner
			}](),
			input: "nm: a\nupper: b\nUpper: c\ntagged: d\ndashed: e\nhidden: f\nembedded: {a: 1}\nEmbedded: {a: 1}\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`3:1: $.Upper~: unknown field "Upper"`,
				`4:1: $.tagged~: unknown field "tagged"`,
				`5:1: $.dashed~: unknown field "dashed"`,
				`6:1: $.hidden~: unknown field "hidden"`,
				`8:1: $.Embedded~: unknown field "Embedded"`,
			),
		},
		"map, pointer, and array": {
			decode: decodeInto[struct {
				M map[string]strictInner `yaml:"m"`
				P *strictInner           `yaml:"p"`
				L [2]strictInner         `yaml:"l"`
			}](),
			input: "m:\n  k:\n    x: 1\n  j:\n    y: 1\np:\n  z: 1\nl:\n  - w: 1\n  - a: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`3:5: $.m.k.x~: unknown field "x"`,
				`5:5: $.m.j.y~: unknown field "y"`,
				`7:3: $.p.z~: unknown field "z"`,
				`9:5: $.l[0].w~: unknown field "w"`,
			),
		},
		// Two elements merge one mapping, and its key reports once, at
		// the place the anchor defines it.
		"merge source read twice": {
			decode: decodeInto[struct {
				Defs    any           `yaml:"defs"`
				Servers []strictInner `yaml:"servers"`
			}](),
			input: "defs: &d\n  zzz: 1\n  a: 1\nservers:\n  - <<: *d\n  - <<: *d\n    yyy: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`2:3: $.defs.zzz~: unknown field "zzz"`,
				`7:5: $.servers[1].yyy~: unknown field "yyy"`,
			),
		},
		"value an alias reads twice": {
			decode: decodeInto[struct {
				A strictInner `yaml:"a"`
				B strictInner `yaml:"b"`
			}](),
			input: "a: &v\n  zzz: 1\n  yyy: 2\nb: *v\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`2:3: $.a.zzz~: unknown field "zzz"`,
				`3:3: $.a.yyy~: unknown field "yyy"`,
			),
		},
		// The decoder allows the prefix, so the keys under it stay out
		// of the report though no field has their names.
		"prefix a go-yaml option allows": {
			decode: decodeInto[struct {
				Name string `yaml:"name"`
			}](),
			input: "name: x\nx-foo: 1\nzzz: 2\nx-bar: 3\nyyy: 4\n",
			opts: []niceyaml.DecodeOption{
				strict,
				niceyaml.WithYAMLDecodeOptions(yaml.AllowFieldPrefixes("x-")),
			},
			want: stringtest.JoinLF(
				`3:1: $.zzz~: unknown field "zzz"`,
				`5:1: $.yyy~: unknown field "yyy"`,
			),
		},
		// The unmarshaler of the type hands its fields to the decoder,
		// which checks them, at the struct and below it.
		"type that decodes itself": {
			decode: decodeInto[struct {
				S strictSelf `yaml:"s"`
			}](),
			input: "s:\n  x: 1\n  zzz: 2\n  yyy: 3\n  n:\n    - a: 1\n      www: 2\n    - vvv: 3\nqqq: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`3:3: $.s.zzz~: unknown field "zzz"`,
				`4:3: $.s.yyy~: unknown field "yyy"`,
				`7:7: $.s.n[0].www~: unknown field "www"`,
				`8:7: $.s.n[1].vvv~: unknown field "vvv"`,
				`9:1: $.qqq~: unknown field "qqq"`,
			),
		},
		"decode into a type that decodes itself": {
			decode: decodeInto[strictSelf](),
			input:  "zzz: 2\nx: 1\nn:\n  - www: 2\n",
			opts:   []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`1:1: $.zzz~: unknown field "zzz"`,
				`4:5: $.n[0].www~: unknown field "www"`,
			),
		},
		// The unmarshaler of the type parses the text with a decoder of
		// its own, which skips unknown fields, so none inside it reports.
		"type that parses its own text": {
			decode: decodeInto[struct {
				S strictText `yaml:"s"`
			}](),
			input: "s:\n  x: 1\n  zzz: 2\n  n:\n    www: 2\nqqq: 1\nppp: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`6:1: $.qqq~: unknown field "qqq"`,
				`7:1: $.ppp~: unknown field "ppp"`,
			),
		},
		// The decoder leaves out the entries the merge key brings in, so
		// neither www nor the field under in reports.
		"struct that ignores merge keys": {
			decode: decodeInto[struct {
				Defs *StrictBase   `yaml:"defs"`
				V    strictAliased `yaml:"v"`
			}](),
			input: "v:\n  <<: {in: {qqq: 1}, www: 1}\n  b: 1\n  yyy: 2\n  zzz: 3\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`4:3: $.v.yyy~: unknown field "yyy"`,
				`5:3: $.v.zzz~: unknown field "zzz"`,
			),
		},
		"struct that reads merge keys": {
			decode: decodeInto[struct {
				V struct {
					In strictInner `yaml:"in"`
					B  int         `yaml:"b"`
				} `yaml:"v"`
			}](),
			input: "v:\n  <<: {in: {qqq: 1}, www: 1}\n  b: 1\n  yyy: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`2:13: $.v.<<.in.qqq~: unknown field "qqq"`,
				`2:22: $.v.<<.www~: unknown field "www"`,
				`4:3: $.v.yyy~: unknown field "yyy"`,
			),
		},
		"interface field": {
			decode: decodeInto[struct {
				S any `yaml:"s"`
			}](),
			input: "s:\n  zzz: 2\nqqq: 1\nppp: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`3:1: $.qqq~: unknown field "qqq"`,
				`4:1: $.ppp~: unknown field "ppp"`,
			),
		},
		// The decoder reports the mismatch and no unknown field, so the
		// decode reports the mismatch alone.
		"type mismatch first": {
			decode: decodeInto[struct {
				T int `yaml:"t"`
			}](),
			input: "t: abc\nqqq: 1\nppp: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want:  `1:4: $.t: expected integer, got string`,
		},
		// The decoder decodes no field from a mapping with a key it does
		// not read as a string, and rejects none of its keys.
		"mapping with a key that is no string": {
			decode: decodeInto[struct {
				A strictInner `yaml:"a"`
				B strictInner `yaml:"b"`
			}](),
			input: "a:\n  zzz: 1\n  yyy: 1\nb:\n  1: x\n  www: 2\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`2:3: $.a.zzz~: unknown field "zzz"`,
				`3:3: $.a.yyy~: unknown field "yyy"`,
			),
		},
		// The paths read from the root of the document.
		"scoped decode": {
			decode: decodeInto[strictInner](),
			input:  "servers:\n  - a: 1\n    zzz: 2\n    yyy: 3\n",
			scope:  "$.servers[0]",
			opts:   []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`3:5: $.servers[0].zzz~: unknown field "zzz"`,
				`4:5: $.servers[0].yyy~: unknown field "yyy"`,
			),
		},
		"go-yaml option": {
			decode: decodeInto[struct {
				Name string `yaml:"name"`
			}](),
			input: "foo: 1\nbar: 2\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithYAMLDecodeOptions(yaml.DisallowUnknownField())},
			want: stringtest.JoinLF(
				`1:1: $.foo~: unknown field "foo"`,
				`2:1: $.bar~: unknown field "bar"`,
			),
		},
		// The later of two entries with one key is the one the decoder
		// reads.
		"duplicate keys": {
			decode: decodeInto[struct {
				Name string `yaml:"name"`
			}](),
			input:      "zzz: 1\nzzz: 2\nyyy: 3\n",
			sourceOpts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			opts:       []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`2:1: $.zzz~: unknown field "zzz"`,
				`3:1: $.yyy~: unknown field "yyy"`,
			),
		},
		"quoted, tagged, and explicit keys": {
			decode: decodeInto[struct {
				Name string `yaml:"name"`
			}](),
			input: "\"q q\": 1\n!!str zzz: 2\n? yyy\n: 3\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`1:1: $.'q q'~: unknown field "q q"`,
				`2:7: $.zzz~: unknown field "zzz"`,
				`3:3: $.yyy~: unknown field "yyy"`,
			),
		},
		// The document reads a second parse, whose tokens the decoder
		// reports.
		"document that reuses an anchor name": {
			decode: decodeInto[struct {
				X int         `yaml:"x"`
				Y int         `yaml:"y"`
				A strictInner `yaml:"a"`
			}](),
			input: "x: &n 1\ny: &n 2\na:\n  zzz: *n\n  yyy: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				`4:3: $.a.zzz~: unknown field "zzz"`,
				`5:3: $.a.yyy~: unknown field "yyy"`,
			),
		},
		"unknown fields allowed": {
			decode: decodeInto[struct {
				Name string `yaml:"name"`
			}](),
			input: "foo: 1\nbar: 2\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rows := strings.Count(tc.want, "\n") + 1

			// The decoder picks the field it reports in no fixed order,
			// so several decodes show that the report does not depend on
			// its pick.
			for range 10 {
				docs, err := niceyaml.NewSourceFromString(tc.input, tc.sourceOpts...).Documents()
				require.NoError(t, err)
				require.Len(t, docs, 1)

				node := docs[0]
				if tc.scope != "" {
					node, err = node.At(paths.MustParse(tc.scope))
					require.NoError(t, err)
				}

				err = tc.decode(t.Context(), node, tc.opts...)
				if tc.want == "" {
					require.NoError(t, err)

					continue
				}

				require.Error(t, err)
				assert.Equal(t, tc.want, rejectionRows(err))

				if rows == 1 {
					require.EqualError(t, err, tc.want)

					continue
				}

				// The message lists each field under the summary, as the
				// tree does.
				require.EqualError(t, err, fmt.Sprintf("%d unknown fields\n%s", rows, tc.want))
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)
				assert.Same(t, node, srcErr.Node())

				// The summary counts the fields and points at none.
				_, ok := srcErr.Path()
				assert.False(t, ok)

				_, ok = srcErr.Range()
				assert.False(t, ok)

				fields := srcErr.Errors()
				require.Len(t, fields, rows)

				for _, field := range fields {
					require.ErrorIs(t, field, niceyaml.ErrDecode)
					require.NoError(t, field.Unresolved())

					path, ok := field.Path()
					require.True(t, ok)
					assert.True(t, strings.HasSuffix(path.String(), "~"), "path %s", path)

					// Each field holds a rejection the decoder returned.
					rejected, ok := errors.AsType[*yaml.UnknownFieldError](field)
					require.True(t, ok)
					assert.Equal(t, rejected.GetMessage(), field.Message())
				}
			}
		})
	}
}

func TestDocument_Decode_UnknownFields_KeyKinds(t *testing.T) {
	t.Parallel()

	// KeyKinds holds two structs. The mapping of A always holds an
	// unknown field, so the decode fails and the Node looks for others.
	// The mapping of B holds the key under test beside the unknown field
	// www.
	type keyKinds struct {
		S any         `yaml:"s"`
		I any         `yaml:"i"`
		A strictInner `yaml:"a"`
		B strictInner `yaml:"b"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The decoder reads the keys of a struct as strings. It decodes
	// nothing from a mapping that holds a key of another kind, and
	// rejects none of its keys, so www reports only beside a key that
	// reads as a string.
	tcs := map[string]struct {
		key  string
		want bool
	}{
		"plain string":          {key: "k: x", want: true},
		"double quoted":         {key: `"k": x`, want: true},
		"single quoted":         {key: "'k': x", want: true},
		"anchored string":       {key: "&anc k: x", want: true},
		"explicit string":       {key: "? k\n  : x", want: true},
		"string tag on an int":  {key: "!!str 1: x", want: true},
		"local tag on a string": {key: "!custom k: x", want: true},
		"local tag on a number": {key: "!custom 1: x", want: true},
		"date without a tag":    {key: "2020-01-01: x", want: true},
		"alias to a string":     {key: "*s : x", want: true},
		"merge key":             {key: "<<: {a: 1}", want: true},
		"integer":               {key: "1: x"},
		"hexadecimal integer":   {key: "0x10: x"},
		"float":                 {key: "1.5: x"},
		"infinity":              {key: ".inf: x"},
		"boolean":               {key: "true: x"},
		"null":                  {key: "~: x"},
		"explicit integer":      {key: "? 1\n  : x"},
		"integer tag":           {key: "!!int 1: x"},
		"float tag":             {key: "!!float 1: x"},
		"boolean tag":           {key: "!!bool true: x"},
		"null tag":              {key: "!!null k: x"},
		"binary tag":            {key: "!!binary YQ==: x"},
		"timestamp tag":         {key: "!!timestamp 2020-01-01: x"},
		"alias to an integer":   {key: "*i : x"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			anchors := "s: &s str\ni: &i 5\n"
			b := "b:\n  www: 2\n  " + tc.key + "\n"

			// The decoder alone says whether it rejects www beside the
			// key.
			alone, err := niceyaml.NewSourceFromString(anchors + b).Document()
			require.NoError(t, err)

			var v keyKinds

			err = alone.DecodeInto(t.Context(), &v, strict)
			if tc.want {
				require.ErrorContains(t, err, `unknown field`)
			} else {
				require.NoError(t, err)
			}

			// The report agrees with it.
			both, err := niceyaml.NewSourceFromString(anchors + "a:\n  zzz: 1\n" + b).Document()
			require.NoError(t, err)

			var w keyKinds

			err = both.DecodeInto(t.Context(), &w, strict)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			rows := rejectionRows(err)
			assert.Contains(t, rows, `$.a.zzz~: unknown field "zzz"`)
			assert.Equal(t, tc.want, strings.Contains(rows, `$.b.www~: unknown field "www"`), rows)
		})
	}
}

func TestDocument_Decode_UnknownFields_IgnoredMerges(t *testing.T) {
	t.Parallel()

	// Aliased holds a struct with an inline field tagged alias and
	// omitempty, for which the decoder leaves out the entries a `<<`
	// merge key brings in. Merged holds the same fields without that
	// field, so the decoder reads those entries.
	type aliased struct {
		Defs *StrictBase   `yaml:"defs"`
		V    strictAliased `yaml:"v"`
	}

	type merged struct {
		V struct {
			In strictInner `yaml:"in"`
			B  int         `yaml:"b"`
		} `yaml:"v"`
		Defs *StrictBase `yaml:"defs"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		merge string
	}{
		"inline mapping": {
			merge: "v:\n  <<: {in: {qqq: 1}, www: 1}\n  b: 1\n",
		},
		"sequence of sources": {
			merge: "v:\n  <<: [{in: {qqq: 1}}, {www: 1}]\n  b: 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decode := func(input string, v any) error {
				doc, err := niceyaml.NewSourceFromString(input).Document()
				require.NoError(t, err)

				return doc.DecodeInto(t.Context(), v, strict)
			}

			// The decoder alone rejects no key the merge brings in for
			// the struct that ignores merges.
			err := decode(tc.merge, &aliased{})
			require.NoError(t, err)

			// With a key of its own that no field reads, the report
			// lists that key and still none the merge brings in.
			err = decode(tc.merge+"  yyy: 2\n", &aliased{})
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			rows := rejectionRows(err)
			assert.Contains(t, rows, `$.v.yyy~: unknown field "yyy"`)
			assert.NotContains(t, rows, "qqq")
			assert.NotContains(t, rows, "www")

			// The decoder reads the same merge for the struct without
			// the field. It rejects a sequence of sources another way,
			// and the report then lists no unknown field.
			err = decode(tc.merge+"  yyy: 2\n", &merged{})
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			rows = rejectionRows(err)
			if strings.Contains(tc.merge, "<<: [") {
				assert.NotContains(t, rows, "unknown field")

				return
			}

			assert.Contains(t, rows, `$.v.yyy~: unknown field "yyy"`)
			assert.Contains(t, rows, `unknown field "qqq"`)
			assert.Contains(t, rows, `unknown field "www"`)
		})
	}
}

func TestDocument_Decode_UnknownFields_Source(t *testing.T) {
	t.Parallel()

	// A named source puts its name in front of the summary and of each
	// field in the message, and each field keeps its own position.
	source := niceyaml.NewSourceFromString("foo: 1\nname: x\nbar: 2\n", niceyaml.WithName("cfg.yaml"))

	_, err := source.Decode[struct {
		Name string `yaml:"name"`
	}](t.Context(), niceyaml.WithDisallowUnknownFields(true))
	require.EqualError(t, err, stringtest.JoinLF(
		"cfg.yaml: 2 unknown fields",
		`cfg.yaml:1:1: $.foo~: unknown field "foo"`,
		`cfg.yaml:3:1: $.bar~: unknown field "bar"`,
	))
	require.ErrorIs(t, err, niceyaml.ErrDecode)

	assert.Equal(t, stringtest.JoinLF(
		"cfg.yaml: 2 unknown fields",
		`|-- 1:1: $.foo~: unknown field "foo"`,
		"`-- "+`3:1: $.bar~: unknown field "bar"`,
		"",
		"   1 | foo: 1",
		`     | ^^^ unknown field "foo"`,
		"     | ...",
		"   3 | bar: 2",
		`     | ^^^ unknown field "bar"`,
	), strings.TrimRight(niceyaml.FormatError(err, 0), "\n"))
}

func TestDecoder_DecodeInto_UnknownFields(t *testing.T) {
	t.Parallel()

	// A Decoder carries the option to every node it decodes.
	dec := niceyaml.NewDecoder(niceyaml.WithDisallowUnknownFields(true))

	docs, err := niceyaml.NewSourceFromString("foo: 1\nbar: 2\n---\nname: x\n---\nbaz: 3\n").Documents()
	require.NoError(t, err)
	require.Len(t, docs, 3)

	want := []string{
		stringtest.JoinLF(
			`1:1: $.foo~: unknown field "foo"`,
			`2:1: $.bar~: unknown field "bar"`,
		),
		"",
		`6:1: $.baz~: unknown field "baz"`,
	}

	for i, doc := range docs {
		var v struct {
			Name string `yaml:"name"`
		}

		err := dec.DecodeInto(t.Context(), doc, &v)
		assert.Equal(t, want[i], rejectionRows(err), "document %d", i)
	}
}
