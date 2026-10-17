package matcher_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestContent(t *testing.T) {
	t.Parallel()

	// TestText_Reads checks that a string want matches the text of each
	// scalar it reads, as written.
	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		want    bool
	}{
		"string match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   `kind: Deployment`,
			want:    true,
		},
		"string no match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   `kind: Service`,
		},
		"missing field": {
			matcher: matcher.Content(missingPath, "value"),
			input:   `kind: Deployment`,
		},
		"string does not match respelled float text": {
			matcher: versionIs("1.1"),
			input:   `version: 1.10`,
		},
		"string does not match respelled float": {
			matcher: versionIs("1"),
			input:   `version: 1.0`,
		},
		"string does not match decoded hex": {
			matcher: versionIs("16"),
			input:   `version: 0x10`,
		},
		"string matches anchored tagged hex text": {
			matcher: versionIs("0x10"),
			input:   `version: &v !!int 0x10`,
			want:    true,
		},
		"string matches hex text behind a tagged alias": {
			matcher: versionIs("0x10"),
			input: stringtest.Input(`
				v: &v 0x10
				version: !t *v
			`),
			want: true,
		},
		"string matches quoted number text": {
			matcher: versionIs("2"),
			input:   `version: "2"`,
			want:    true,
		},
		"string does not match the text of a float-tagged quoted scalar": {
			matcher: versionIs("1.10"),
			input:   `version: !!float "1.10"`,
		},
		"string does not match respelled infinity": {
			matcher: versionIs("+Inf"),
			input:   `version: .inf`,
		},
		"string matches nan text as written": {
			matcher: versionIs(".nan"),
			input:   `version: .nan`,
			want:    true,
		},
		"string does not match respelled bool": {
			matcher: versionIs("true"),
			input:   `version: True`,
		},
		"named string with a method matches float text as written": {
			matcher: versionIs(methodString("1.10")),
			input:   `version: 1.10`,
			want:    true,
		},
		"named string with a method does not match respelled float": {
			matcher: versionIs(methodString("1.1")),
			input:   `version: 1.10`,
		},
		"named string with a method matches hex text as written": {
			matcher: versionIs(methodString("0x10")),
			input:   `version: 0x10`,
			want:    true,
		},
		"self-decoding string matches its own decode": {
			matcher: versionIs(prefixedString("v1.1")),
			input:   `version: 1.10`,
			want:    true,
		},
		"self-decoding string does not match text as written": {
			matcher: versionIs(prefixedString("v1.10")),
			input:   `version: 1.10`,
		},
		"self-decoding string matches text its decode changes": {
			matcher: matcher.Content(kindPath, lowerString("deployment")),
			input:   `kind: Deployment`,
			want:    true,
		},
		"self-decoding integer matches the name it decodes from": {
			matcher: versionIs(slog.LevelInfo),
			input:   `version: INFO`,
			want:    true,
		},
		"self-decoding integer does not match the number it holds": {
			// A slog.Level decodes from the name of a level, and 0 names
			// none.
			matcher: versionIs(slog.LevelInfo),
			input:   `version: 0`,
		},
		"float matches unquoted float": {
			matcher: versionIs(1.0),
			input:   `version: 1.0`,
			want:    true,
		},
		"float matches integer spelling": {
			matcher: versionIs(1.0),
			input:   `version: 1`,
			want:    true,
		},
		"int no match": {
			matcher: versionIs(2),
			input:   `version: 3`,
		},
		"int matches float spelling": {
			matcher: versionIs(1),
			input:   `version: 1.0`,
			want:    true,
		},
		"int matches hex spelling": {
			matcher: versionIs(16),
			input:   `version: 0x10`,
			want:    true,
		},
		"int matches an int-tagged integer": {
			matcher: versionIs(16),
			input:   `version: !!int 16`,
			want:    true,
		},
		"int does not match other int-tagged integer": {
			matcher: versionIs(16),
			input:   `version: !!int 17`,
		},
		"int64 matches an int-tagged hex integer": {
			matcher: versionIs(int64(16)),
			input:   `version: !!int 0x10`,
			want:    true,
		},
		"uint64 matches an int-tagged integer": {
			matcher: versionIs(uint64(16)),
			input:   `version: !!int 16`,
			want:    true,
		},
		"int matches an anchored int-tagged integer": {
			matcher: versionIs(16),
			input:   `version: &v !!int 0x10`,
			want:    true,
		},
		"int matches an alias to an int-tagged integer": {
			matcher: versionIs(16),
			input: stringtest.Input(`
				base: &k !!int 16
				version: *k
			`),
			want: true,
		},
		"int does not match a fraction": {
			matcher: versionIs(1),
			input:   `version: 1.5`,
		},
		"int does not match a plain exponent fraction": {
			// The decoder reads 25e-1 as a string and truncates it to 2.
			matcher: versionIs(2),
			input:   `version: 25e-1`,
		},
		"int does not match a quoted fraction": {
			matcher: versionIs(2),
			input:   `version: "2.5"`,
		},
		"int does not match a double-quoted integer": {
			matcher: versionIs(2),
			input:   `version: "2"`,
		},
		"int does not match a single-quoted integer": {
			matcher: versionIs(2),
			input:   `version: '2'`,
		},
		"int does not match a str-tagged integer": {
			matcher: versionIs(2),
			input:   `version: !!str 2`,
		},
		"int does not match an anchored str-tagged integer": {
			matcher: versionIs(2),
			input:   `version: &v !!str 2`,
		},
		"int does not match an aliased quoted integer": {
			matcher: versionIs(2),
			input: stringtest.Input(`
				v: &v "2"
				version: *v
			`),
		},
		"int does not match a quoted integer behind a tagged alias": {
			matcher: versionIs(2),
			input: stringtest.Input(`
				v: &v "2"
				version: !t *v
			`),
		},
		"int does not match a str-tagged integer behind a tagged alias": {
			matcher: versionIs(2),
			input: stringtest.Input(`
				v: &v !!str 2
				version: !t *v
			`),
		},
		"int matches a plain integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   `2: x`,
			want:    true,
		},
		"int does not match a quoted integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   `"2": x`,
		},
		"int does not match a str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   `!!str 2: x`,
		},
		"int does not match an explicit str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input: stringtest.Input(`
				? !!str 2
				: x
			`),
		},
		"int does not match an anchored str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   `&k !!str 2: x`,
		},
		"float does not match a str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2.0),
			input:   `!!str 2: x`,
		},
		"string matches a str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, "2"),
			input:   `!!str 2: x`,
			want:    true,
		},
		"int does not match a verbatim str-tagged integer": {
			matcher: versionIs(2),
			input:   `version: !<tag:yaml.org,2002:str> 2`,
		},
		"int does not match a block scalar": {
			matcher: versionIs(2),
			input: stringtest.Input(`
				version: |-
				  2
			`),
		},
		"int does not match a quoted exponent": {
			matcher: versionIs(1000),
			input:   `version: "1e3"`,
		},
		"float does not match a quoted integer": {
			matcher: versionIs(2.0),
			input:   `version: '2'`,
		},
		"float does not match a str-tagged integer": {
			matcher: versionIs(2.0),
			input:   `version: !!str 2`,
		},
		"float matches a float-tagged quoted integer": {
			matcher: versionIs(2.0),
			input:   `version: !!float '2'`,
			want:    true,
		},
		"int matches a plain exponent spelling": {
			matcher: versionIs(1000),
			input:   `version: 1e3`,
			want:    true,
		},
		"float NaN matches nan": {
			matcher: versionIs(math.NaN()),
			input:   `version: .nan`,
			want:    true,
		},
		"float NaN matches NaN spelling": {
			matcher: versionIs(math.NaN()),
			input:   `version: .NaN`,
			want:    true,
		},
		"float NaN does not match number": {
			matcher: versionIs(math.NaN()),
			input:   `version: 1.5`,
		},
		"float NaN does not match quoted nan": {
			matcher: versionIs(math.NaN()),
			input:   `version: ".nan"`,
		},
		"float does not match nan": {
			matcher: versionIs(1.5),
			input:   `version: .nan`,
		},
		"float does not match plain inf": {
			matcher: versionIs(math.Inf(1)),
			input:   `version: inf`,
		},
		"float does not match Infinity": {
			matcher: versionIs(math.Inf(1)),
			input:   `version: Infinity`,
		},
		"float NaN does not match plain nan": {
			matcher: versionIs(math.NaN()),
			input:   `version: nan`,
		},
		"float does not match a hex float": {
			matcher: versionIs(0.25),
			input:   `version: 0x1p-2`,
		},
		"int does not match a hex float": {
			matcher: versionIs(1),
			input:   `version: 0x1p0`,
		},
		"float matches a signed plain exponent": {
			matcher: versionIs(-1000.0),
			input:   `version: -1e3`,
			want:    true,
		},
		"float32 inf does not match an overflowing plain exponent": {
			// The decoder reads 1e39 as a string and narrows it to +Inf.
			matcher: versionIs(float32(math.Inf(1))),
			input:   `version: 1e39`,
		},
		"float32 inf does not match an overflowing float": {
			matcher: versionIs(float32(math.Inf(1))),
			input:   `version: 4.0e+38`,
		},
		"float32 negative inf does not match an overflowing float": {
			matcher: versionIs(float32(math.Inf(-1))),
			input:   `version: -1.0e+39`,
		},
		"float32 inf matches inf": {
			matcher: versionIs(float32(math.Inf(1))),
			input:   `version: .inf`,
			want:    true,
		},
		"float32 negative inf matches negative inf": {
			matcher: versionIs(float32(math.Inf(-1))),
			input:   `version: -.inf`,
			want:    true,
		},
		"empty string does not match null": {
			matcher: matcher.Content(kindPath, ""),
			input:   `kind:`,
		},
		"false does not match null": {
			matcher: matcher.Content(enabledPath, false),
			input:   `enabled: null`,
		},
		"large int does not match a rounded float": {
			matcher: versionIs(int64(9007199254740993)),
			input:   `version: 9007199254740992.0`,
		},
		"large int matches its float spelling": {
			matcher: versionIs(int64(9007199254740992)),
			input:   `version: 9007199254740992.0`,
			want:    true,
		},
		"negative int does not match unsigned": {
			matcher: versionIs(-1),
			input:   `version: 18446744073709551615`,
		},
		"float matches a plain exponent": {
			matcher: versionIs(1000.0),
			input:   `version: 1e3`,
			want:    true,
		},
		"float matches a plain exponent fraction": {
			matcher: versionIs(2.5),
			input:   `version: 25e-1`,
			want:    true,
		},
		"float matches a large plain exponent": {
			matcher: versionIs(1e19),
			input:   `version: 1e19`,
			want:    true,
		},
		"float does not match a quoted exponent": {
			matcher: versionIs(1000.0),
			input:   `version: "1e3"`,
		},
		"float NaN does not match plain NaN": {
			matcher: versionIs(math.NaN()),
			input:   `version: NaN`,
		},
		"string matches plain inf": {
			matcher: versionIs("inf"),
			input:   `version: inf`,
			want:    true,
		},
		"named int does not match other integer": {
			matcher: versionIs(namedInt(1)),
			input:   `version: 2`,
		},
		"named float matches a plain exponent": {
			// The decoder reads 1e3 as a string, which go-yaml cannot set
			// a named float from.
			matcher: versionIs(namedFloat(1000)),
			input:   `version: 1e3`,
			want:    true,
		},
		"named float32 matches a plain exponent": {
			matcher: versionIs(namedFloat32(1000)),
			input:   `version: 1e3`,
			want:    true,
		},
		"named float matches a plain exponent fraction": {
			matcher: versionIs(namedFloat(2.5)),
			input:   `version: 25e-1`,
			want:    true,
		},
		"named float does not match a quoted exponent": {
			matcher: versionIs(namedFloat(1000)),
			input:   `version: "1e3"`,
		},
		"duration matches plain": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: 5s`,
			want:    true,
		},
		"duration matches double-quoted": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: "5s"`,
			want:    true,
		},
		"duration matches single-quoted": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: '5s'`,
			want:    true,
		},
		"duration matches block scalar": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input: stringtest.LinesLF(
				"timeout: |-",
				"  5s",
			),
			want: true,
		},
		"duration matches str tag": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: !!str 5s`,
			want:    true,
		},
		"duration does not match other duration": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: "6s"`,
		},
		"duration does not match an integer of nanoseconds": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: 5000000000`,
		},
		"duration does not match plain float": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: 1.5e3`,
		},
		"duration does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: 1e3`,
		},
		"duration does not match negative plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: -1e3`,
		},
		"duration does not match anchored plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: &a 2E+3`,
		},
		"duration does not match overflowing plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: 1e999`,
		},
		"duration does not match overflowing plain float": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   `timeout: 1.5e999`,
		},
		"duration does not match plain words": {
			// A string that time.ParseDuration rejects does not read as a
			// time.Duration, and the decode reports it with
			// niceyaml.ErrDecode, so the matcher answers no rather than
			// returning the error. A quoted float is such a string.
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   `timeout: 5 minutes`,
		},
		"duration does not match a quoted exponent": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   `timeout: "1e3"`,
		},
		"duration does not match a str-tagged exponent": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   `timeout: !!str 1e3`,
		},
		"duration does not match plain inf": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   `timeout: inf`,
		},
		"bool no match": {
			matcher: matcher.Content(enabledPath, true),
			input:   `enabled: false`,
		},
		"bool matches a capitalized spelling": {
			matcher: matcher.Content(enabledPath, true),
			input:   `enabled: True`,
			want:    true,
		},
		"bool matches a bool-tagged quoted scalar": {
			matcher: matcher.Content(enabledPath, true),
			input:   `enabled: !!bool "true"`,
			want:    true,
		},
		"bool does not match string": {
			matcher: matcher.Content(enabledPath, true),
			input:   `enabled: "true"`,
		},
		"bool does not match a tag over no value": {
			matcher: matcher.Content(enabledPath, true),
			input:   "enabled: !!bool\n",
		},
		"lowest int64 does not match negative infinity": {
			matcher: versionIs(int64(math.MinInt64)),
			input:   `version: -.inf`,
		},
		"highest int64 does not match a larger plain exponent": {
			matcher: versionIs(int64(math.MaxInt64)),
			input:   `version: 1e19`,
		},
		"lowest int64 does not match a smaller float": {
			matcher: versionIs(int64(math.MinInt64)),
			input:   `version: -1.0e+19`,
		},
		"highest uint64 does not match 2^64": {
			matcher: versionIs(uint64(math.MaxUint64)),
			input:   `version: 18446744073709551616.0`,
		},
		"self-decoding integer reads a float its own way": {
			matcher: versionIs(millis(2000)),
			input:   `version: 2.0`,
			want:    true,
		},
		"self-decoding integer never matches a fraction": {
			matcher: versionIs(millis(2500)),
			input:   `version: 2.5`,
		},
		"value that does not decode": {
			matcher: versionIs(1),
			input:   `version: abc`,
		},
		"mapping does not decode into scalar": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input: stringtest.Input(`
				kind:
				  name: Deployment
			`),
		},
		"sequence does not decode into scalar": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   `kind: [Deployment]`,
		},
		"nested path match": {
			matcher: matcher.Content(metadataName, "my-app"),
			input: stringtest.Input(`
				kind: Deployment
				metadata:
				  name: my-app
			`),
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			assert.Equal(t, tc.want, match(t, tc.matcher, doc))
		})
	}

	t.Run("duplicate key matches the value the decode keeps", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Pod
			kind: Service
		`), niceyaml.WithAllowDuplicateKeys(true))

		assert.True(t, match(t, matcher.Content(kindPath, "Service"), doc))
		assert.False(t, match(t, matcher.Content(kindPath, "Pod"), doc))
	})

	t.Run("node a decode hands its validator", func(t *testing.T) {
		t.Parallel()

		// The Node a validator gets decodes with the settings of its
		// source, so a decode of it reads an alias to an anchor of a
		// reference document. The path of a matcher resolves in the
		// document alone, so it cannot follow such an alias. Each case
		// matches a version of 2.
		refs := yamltest.References("r: &r 2")

		tcs := map[string]struct {
			err   error
			input string
			want  bool
		}{
			"value beside an alias to a reference anchor matches": {
				input: stringtest.Input(`
					other: *r
					version: 2
				`),
				want: true,
			},
			"alias to a reference anchor is an error":        {input: `version: *r`, err: paths.ErrAlias},
			"tagged alias to a reference anchor is an error": {input: `version: !!int *r`, err: paths.ErrAlias},
			"alias that leads to a tagged alias to a reference anchor is an error": {
				input: stringtest.Input(`
					x: &x !!int *r
					version: *x
				`),
				err: paths.ErrAlias,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, refs)

				got, err := matchInDecode(t, versionIs(2), doc)
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	testCannotDecide(t, func(path paths.Path) matcher.Matcher {
		return matcher.Content(path, "Deployment")
	})

	testAliasBomb(t, matcher.Content(kindPath, "Deployment"))

	t.Run("scalar aliases written out as text", func(t *testing.T) {
		t.Parallel()

		// A type that decodes itself from text or from YAML bytes gets
		// the node written out, with a copy of the long scalar for each
		// alias. A plain string reads the list as a value, which shares
		// the scalar, so it does not match.
		input := stringtest.LinesLF(
			"a: &a "+strings.Repeat("x", 2000),
			"kind: "+yamltest.FlowSequence("*a", 500),
		)

		tcs := map[string]struct {
			m    matcher.Matcher
			err  error
			opts []niceyaml.SourceOption
		}{
			"text unmarshaler": {
				m:   matcher.Content(kindPath, prefixedString("vx")),
				err: schema.ErrExcessiveAliasing,
			},
			"text unmarshaler in a source with the limit off": {
				m:    matcher.Content(kindPath, prefixedString("vx")),
				opts: []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
			},
			"bytes unmarshaler": {
				m:   matcher.Content(kindPath, millis(1000)),
				err: schema.ErrExcessiveAliasing,
			},
			"unmarshaler that decodes into text unmarshalers": {
				m:   matcher.Content(kindPath, forwardedText("vx")),
				err: schema.ErrExcessiveAliasing,
			},
			"plain string": {
				m: matcher.Content(kindPath, "x"),
			},
			"named string": {
				m: matcher.Content(kindPath, methodString("x")),
			},
			"duration": {
				m: matcher.Content(kindPath, time.Second),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, input, tc.opts...)

				ok, err := tc.m.Match(t.Context(), doc)
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
					assert.True(t, niceyaml.IsInvalid(err))

					var bound *niceyaml.SourceError

					require.ErrorAs(t, err, &bound)

					_, positioned := bound.Position()
					assert.True(t, positioned)
				} else {
					require.NoError(t, err)
				}

				assert.False(t, ok)
			})
		}
	})
}

func TestContent_Kinds(t *testing.T) {
	t.Parallel()

	// A want of each kind in matcher.Scalar matches the scalar that holds
	// its value, and so does a type defined on that kind.
	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
	}{
		"string":        {matcher: versionIs("2"), input: `version: 2`},
		"bool":          {matcher: versionIs(true), input: `version: true`},
		"int":           {matcher: versionIs(2), input: `version: 2`},
		"int8":          {matcher: versionIs(int8(2)), input: `version: 2`},
		"int16":         {matcher: versionIs(int16(2)), input: `version: 2`},
		"int32":         {matcher: versionIs(int32(2)), input: `version: 2`},
		"int64":         {matcher: versionIs(int64(2)), input: `version: 2`},
		"uint":          {matcher: versionIs(uint(2)), input: `version: 2`},
		"uint8":         {matcher: versionIs(uint8(2)), input: `version: 2`},
		"uint16":        {matcher: versionIs(uint16(2)), input: `version: 2`},
		"uint32":        {matcher: versionIs(uint32(2)), input: `version: 2`},
		"uint64":        {matcher: versionIs(uint64(2)), input: `version: 2`},
		"uintptr":       {matcher: versionIs(uintptr(2)), input: `version: 2`},
		"float32":       {matcher: versionIs(float32(2.5)), input: `version: 2.5`},
		"float64":       {matcher: versionIs(2.5), input: `version: 2.5`},
		"named string":  {matcher: versionIs(methodString("2")), input: `version: 2`},
		"named bool":    {matcher: versionIs(namedBool(true)), input: `version: true`},
		"named int":     {matcher: versionIs(namedInt(2)), input: `version: 2`},
		"named uint8":   {matcher: versionIs(namedUint8(2)), input: `version: 2`},
		"named float32": {matcher: versionIs(namedFloat32(2.5)), input: `version: 2.5`},
		"named float64": {matcher: versionIs(namedFloat(2.5)), input: `version: 2.5`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.True(t, match(t, tc.matcher, yamltest.FirstDocument(t, tc.input)))
			assert.False(t, match(t, tc.matcher, yamltest.FirstDocument(t, `version: other`)))
		})
	}
}

func TestContent_UnmarshalerError(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, `kind: Deployment`)

	t.Run("value that rejects itself does not match", func(t *testing.T) {
		t.Parallel()

		// The decode of a value that rejects itself fails with
		// niceyaml.ErrDecode, as a rejection of the decoder does, so the
		// matcher answers no.
		ok, err := matcher.Content(kindPath, rejecting("")).Match(t.Context(), doc)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("context error a value wraps comes back", func(t *testing.T) {
		t.Parallel()

		// The error of a context that ended is no answer about the value,
		// so the matcher cannot decide.
		ok, err := matcher.Content(kindPath, stopping("")).Match(t.Context(), doc)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
		assert.False(t, ok)
	})

	t.Run("panic in a value comes back", func(t *testing.T) {
		t.Parallel()

		// A panic is a bug in the method and no answer about the value,
		// so the matcher cannot decide.
		ok, err := matcher.Content(kindPath, panicking("")).Match(t.Context(), doc)
		require.EqualError(t, err, "1:7: decoder panicked: kind registry is empty")
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
		assert.False(t, ok)

		var p *niceyaml.PanicError

		require.ErrorAs(t, err, &p)
	})

	t.Run("panic beside a rejection comes back", func(t *testing.T) {
		t.Parallel()

		// The error matches niceyaml.ErrDecode through the rejection it
		// holds beside the panic, and the panic still decides.
		ok, err := matcher.Content(kindPath, nesting("")).Match(t.Context(), doc)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
		assert.False(t, ok)

		var p *niceyaml.PanicError

		require.ErrorAs(t, err, &p)
	})
}

func TestContent_SelfValidator(t *testing.T) {
	t.Parallel()

	// A want with a Validate method validates the value the matcher
	// reads, as a decode of its type does. A value that fails is the
	// fault of the document, so the matcher returns the error rather
	// than a no, whatever the want holds.
	tcs := map[string]struct {
		matcher matcher.Matcher
		err     error
		input   string
		want    bool
	}{
		"valid value matches": {
			matcher: versionIs(evenInt(2)),
			input:   `version: 2`,
			want:    true,
		},
		"valid value does not match another": {
			matcher: versionIs(evenInt(2)),
			input:   `version: 4`,
		},
		"invalid value is an error": {
			matcher: versionIs(evenInt(2)),
			input:   `version: 3`,
			err:     errOdd,
		},
		"invalid value is an error for a want that equals it": {
			matcher: versionIs(evenInt(3)),
			input:   `version: 3`,
			err:     errOdd,
		},
		"value the decoder rejects does not match": {
			matcher: versionIs(evenInt(2)),
			input:   `version: three`,
		},
		"null does not match": {
			matcher: versionIs(evenInt(2)),
			input:   `version: null`,
		},
		"named float validates a plain exponent": {
			matcher: versionIs(share(1000)),
			input:   `version: 1e3`,
			want:    true,
		},
		"named float rejects a negative plain exponent": {
			matcher: versionIs(share(1000)),
			input:   `version: -1e3`,
			err:     errNegative,
		},
		"self-decoding value validates its own decode": {
			matcher: versionIs(checkedString("v:good")),
			input:   `version: good`,
			want:    true,
		},
		"self-decoding value rejects its own decode": {
			matcher: versionIs(checkedString("v:bad")),
			input:   `version: bad`,
			err:     errChecked,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			ok, err := tc.matcher.Match(t.Context(), doc)
			assert.Equal(t, tc.want, ok)

			if tc.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.err)
			require.NotErrorIs(t, err, niceyaml.ErrDecode)
			assert.True(t, niceyaml.IsInvalid(err))
		})
	}
}

// versionIs returns a [matcher.Content] for the version of a document,
// for a want of any type [matcher.Scalar] admits.
func versionIs[T matcher.Scalar](want T) matcher.Matcher {
	return matcher.Content(versionPath, want)
}

// matchInDecode runs m on the Node that a decode of doc hands its
// validator, and returns what m returns.
func matchInDecode(t *testing.T, m matcher.Matcher, doc *niceyaml.Node) (bool, error) {
	t.Helper()

	var (
		got      bool
		matchErr error
	)

	validator := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		got, matchErr = m.Match(ctx, n)

		return nil
	})

	_, err := doc.Decode[any](t.Context(), niceyaml.WithValidator(validator))
	require.NoError(t, err)

	return got, matchErr //nolint:wrapcheck // The test inspects the error of the match.
}

// methodString is a string type with a method that plays no part in
// decoding, so it decodes as a plain string does.
type methodString string

func (m methodString) Major() string {
	major, _, _ := strings.Cut(string(m), ".")

	return major
}

// prefixedString decodes itself from the text the decoder hands it, and
// puts a "v" in front of that text.
type prefixedString string

func (p *prefixedString) UnmarshalText(text []byte) error {
	*p = prefixedString("v" + string(text))

	return nil
}

// lowerString decodes itself from the text the decoder hands it, in
// lower case.
type lowerString string

func (l *lowerString) UnmarshalText(text []byte) error {
	*l = lowerString(strings.ToLower(string(text)))

	return nil
}

// forwardedText decodes its node into a list of [prefixedString]
// through the function go-yaml hands its UnmarshalYAML, and joins the
// list. Its type reaches no text type, so only its method leads the
// decoder to one.
type forwardedText string

func (f *forwardedText) UnmarshalYAML(unmarshal func(any) error) error {
	var items []prefixedString

	err := unmarshal(&items)
	if err != nil {
		return err
	}

	for _, item := range items {
		*f += forwardedText(item)
	}

	return nil
}

// The named types below have no methods, so each decodes as the basic
// type of its kind does.
type (
	namedBool    bool
	namedInt     int
	namedUint8   uint8
	namedFloat   float64
	namedFloat32 float32
)

// millis decodes itself from seconds, which it holds as milliseconds.
type millis int64

func (m *millis) UnmarshalYAML(data []byte) error {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return fmt.Errorf("read seconds: %w", err)
	}

	*m = millis(seconds * 1000)

	return nil
}

// The errors the types below report.
var (
	// The error rejecting reports from its own decode.
	errRejecting = errors.New("value rejected itself")

	// The errors evenInt, share, and checkedString report from Validate.
	errOdd      = errors.New("odd")
	errNegative = errors.New("negative")
	errChecked  = errors.New("bad")
)

// rejecting decodes itself and reports errRejecting, so the decoder
// returns the value's own error rather than a rejection of its own.
type rejecting string

func (*rejecting) UnmarshalYAML([]byte) error {
	return errRejecting
}

// stopping decodes itself and reports the error of a deadline of its
// own, which the context of the match has not reached.
type stopping string

func (*stopping) UnmarshalYAML([]byte) error {
	return fmt.Errorf("lookup stopped: %w", context.DeadlineExceeded)
}

// panicking decodes itself from text by panicking.
type panicking string

func (*panicking) UnmarshalText([]byte) error {
	panic("kind registry is empty")
}

// nesting decodes itself with a decode of its own and returns the error
// of that decode, which holds a panic beside a rejection.
type nesting string

func (*nesting) UnmarshalYAML(ctx context.Context, _ []byte) error {
	_, err := niceyaml.NewSourceFromString("port: abc\nkind: x\n").Decode[struct {
		Port int       `yaml:"port"`
		Kind panicking `yaml:"kind"`
	}](ctx)

	return err
}

// evenInt is a plain integer that validates itself, and accepts only an
// even value.
type evenInt int

func (e evenInt) Validate() error {
	if e%2 != 0 {
		return errOdd
	}

	return nil
}

// share is a plain float that validates itself, and accepts no negative
// value.
type share float64

func (s share) Validate() error {
	if s < 0 {
		return errNegative
	}

	return nil
}

// checkedString decodes itself from the text the decoder hands it with
// a "v:" in front, and validates the value that gives.
type checkedString string

func (c *checkedString) UnmarshalText(text []byte) error {
	*c = checkedString("v:" + string(text))

	return nil
}

func (c checkedString) Validate() error {
	if c == "v:bad" {
		return errChecked
	}

	return nil
}
