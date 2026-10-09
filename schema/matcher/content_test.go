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

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		want    bool
	}{
		"string match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: Deployment`),
			want:    true,
		},
		"string no match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: Service`),
			want:    false,
		},
		"missing field": {
			matcher: matcher.Content(missingPath, "value"),
			input:   stringtest.Input(`kind: Deployment`),
			want:    false,
		},
		"string matches number text": {
			matcher: matcher.Content(versionPath, "2"),
			input:   stringtest.Input(`version: 2`),
			want:    true,
		},
		"string matches float text as written": {
			matcher: matcher.Content(versionPath, "1.10"),
			input:   stringtest.Input(`version: 1.10`),
			want:    true,
		},
		"string does not match respelled float text": {
			matcher: matcher.Content(versionPath, "1.1"),
			input:   stringtest.Input(`version: 1.10`),
			want:    false,
		},
		"string matches trailing zero as written": {
			matcher: matcher.Content(versionPath, "1.0"),
			input:   stringtest.Input(`version: 1.0`),
			want:    true,
		},
		"string does not match respelled float": {
			matcher: matcher.Content(versionPath, "1"),
			input:   stringtest.Input(`version: 1.0`),
			want:    false,
		},
		"string matches hex text as written": {
			matcher: matcher.Content(versionPath, "0x10"),
			input:   stringtest.Input(`version: 0x10`),
			want:    true,
		},
		"string does not match decoded hex": {
			matcher: matcher.Content(versionPath, "16"),
			input:   stringtest.Input(`version: 0x10`),
			want:    false,
		},
		"string matches anchored float text": {
			matcher: matcher.Content(versionPath, "1.10"),
			input:   stringtest.Input(`version: &v 1.10`),
			want:    true,
		},
		"string matches tagged float text": {
			matcher: matcher.Content(versionPath, "1.10"),
			input:   stringtest.Input(`version: !!float 1.10`),
			want:    true,
		},
		"string matches anchored tagged hex text": {
			matcher: matcher.Content(versionPath, "0x10"),
			input:   stringtest.Input(`version: &v !!int 0x10`),
			want:    true,
		},
		"string matches aliased float text": {
			matcher: matcher.Content(versionPath, "1.10"),
			input: stringtest.Input(`
				v: &v 1.10
				version: *v
			`),
			want: true,
		},
		"string matches float text behind a tagged alias": {
			matcher: matcher.Content(versionPath, "1.10"),
			input: stringtest.Input(`
				v: &v 1.10
				version: !t *v
			`),
			want: true,
		},
		"string matches hex text behind a tagged alias": {
			matcher: matcher.Content(versionPath, "0x10"),
			input: stringtest.Input(`
				v: &v 0x10
				version: !t *v
			`),
			want: true,
		},
		"string matches quoted text": {
			matcher: matcher.Content(versionPath, "1.10"),
			input:   stringtest.Input(`version: "1.10"`),
			want:    true,
		},
		"string matches quoted number text": {
			matcher: matcher.Content(versionPath, "2"),
			input:   stringtest.Input(`version: "2"`),
			want:    true,
		},
		"string matches the character a quoted escape names": {
			matcher: matcher.Content(versionPath, "a\tb"),
			input:   stringtest.Input(`version: "a\tb"`),
			want:    true,
		},
		"string matches a block scalar with its newline": {
			matcher: matcher.Content(versionPath, "1.10\n"),
			input:   "version: |\n  1.10\n",
			want:    true,
		},
		"string matches the respelling of a float-tagged quoted scalar": {
			// The tag makes the quoted text a float, which the decoder
			// respells.
			matcher: matcher.Content(versionPath, "1.1"),
			input:   stringtest.Input(`version: !!float "1.10"`),
			want:    true,
		},
		"string does not match the text of a float-tagged quoted scalar": {
			matcher: matcher.Content(versionPath, "1.10"),
			input:   stringtest.Input(`version: !!float "1.10"`),
			want:    false,
		},
		"string matches infinity text as written": {
			matcher: matcher.Content(versionPath, ".inf"),
			input:   stringtest.Input(`version: .inf`),
			want:    true,
		},
		"string does not match respelled infinity": {
			matcher: matcher.Content(versionPath, "+Inf"),
			input:   stringtest.Input(`version: .inf`),
			want:    false,
		},
		"string matches nan text as written": {
			matcher: matcher.Content(versionPath, ".nan"),
			input:   stringtest.Input(`version: .nan`),
			want:    true,
		},
		"string matches bool text as written": {
			matcher: matcher.Content(versionPath, "True"),
			input:   stringtest.Input(`version: True`),
			want:    true,
		},
		"string does not match respelled bool": {
			matcher: matcher.Content(versionPath, "true"),
			input:   stringtest.Input(`version: True`),
			want:    false,
		},
		"named string with a method matches float text as written": {
			matcher: matcher.Content(versionPath, methodString("1.10")),
			input:   stringtest.Input(`version: 1.10`),
			want:    true,
		},
		"named string with a method does not match respelled float": {
			matcher: matcher.Content(versionPath, methodString("1.1")),
			input:   stringtest.Input(`version: 1.10`),
			want:    false,
		},
		"named string with a method matches hex text as written": {
			matcher: matcher.Content(versionPath, methodString("0x10")),
			input:   stringtest.Input(`version: 0x10`),
			want:    true,
		},
		"self-decoding string matches its own decode": {
			matcher: matcher.Content(versionPath, prefixedString("v1.1")),
			input:   stringtest.Input(`version: 1.10`),
			want:    true,
		},
		"self-decoding string does not match text as written": {
			matcher: matcher.Content(versionPath, prefixedString("v1.10")),
			input:   stringtest.Input(`version: 1.10`),
			want:    false,
		},
		"self-decoding string matches text its decode changes": {
			matcher: matcher.Content(kindPath, lowerString("deployment")),
			input:   stringtest.Input(`kind: Deployment`),
			want:    true,
		},
		"self-decoding integer matches the name it decodes from": {
			matcher: matcher.Content(versionPath, slog.LevelInfo),
			input:   stringtest.Input(`version: INFO`),
			want:    true,
		},
		"self-decoding integer does not match the number it holds": {
			// A slog.Level decodes from the name of a level, and 0 names
			// none.
			matcher: matcher.Content(versionPath, slog.LevelInfo),
			input:   stringtest.Input(`version: 0`),
			want:    false,
		},
		"float matches unquoted float": {
			matcher: matcher.Content(versionPath, 1.0),
			input:   stringtest.Input(`version: 1.0`),
			want:    true,
		},
		"float matches integer spelling": {
			matcher: matcher.Content(versionPath, 1.0),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"int no match": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: 3`),
			want:    false,
		},
		"int matches float spelling": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: 1.0`),
			want:    true,
		},
		"int matches hex spelling": {
			matcher: matcher.Content(versionPath, 16),
			input:   stringtest.Input(`version: 0x10`),
			want:    true,
		},
		"int matches an int-tagged integer": {
			matcher: matcher.Content(versionPath, 16),
			input:   stringtest.Input(`version: !!int 16`),
			want:    true,
		},
		"int does not match other int-tagged integer": {
			matcher: matcher.Content(versionPath, 16),
			input:   stringtest.Input(`version: !!int 17`),
			want:    false,
		},
		"int64 matches an int-tagged hex integer": {
			matcher: matcher.Content(versionPath, int64(16)),
			input:   stringtest.Input(`version: !!int 0x10`),
			want:    true,
		},
		"uint64 matches an int-tagged integer": {
			matcher: matcher.Content(versionPath, uint64(16)),
			input:   stringtest.Input(`version: !!int 16`),
			want:    true,
		},
		"int matches an anchored int-tagged integer": {
			matcher: matcher.Content(versionPath, 16),
			input:   stringtest.Input(`version: &v !!int 0x10`),
			want:    true,
		},
		"int matches an alias to an int-tagged integer": {
			matcher: matcher.Content(versionPath, 16),
			input: stringtest.Input(`
				base: &k !!int 16
				version: *k
			`),
			want: true,
		},
		"int does not match a fraction": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: 1.5`),
			want:    false,
		},
		"int does not match a plain exponent fraction": {
			// The decoder reads 25e-1 as a string and truncates it to 2.
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: 25e-1`),
			want:    false,
		},
		"int does not match a quoted fraction": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: "2.5"`),
			want:    false,
		},
		"int does not match a double-quoted integer": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: "2"`),
			want:    false,
		},
		"int does not match a single-quoted integer": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: '2'`),
			want:    false,
		},
		"int does not match a str-tagged integer": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: !!str 2`),
			want:    false,
		},
		"int does not match an anchored str-tagged integer": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: &v !!str 2`),
			want:    false,
		},
		"int does not match an aliased quoted integer": {
			matcher: matcher.Content(versionPath, 2),
			input: stringtest.Input(`
				v: &v "2"
				version: *v
			`),
			want: false,
		},
		"int does not match a quoted integer behind a tagged alias": {
			matcher: matcher.Content(versionPath, 2),
			input: stringtest.Input(`
				v: &v "2"
				version: !t *v
			`),
			want: false,
		},
		"int does not match a str-tagged integer behind a tagged alias": {
			matcher: matcher.Content(versionPath, 2),
			input: stringtest.Input(`
				v: &v !!str 2
				version: !t *v
			`),
			want: false,
		},
		"int matches a plain integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   stringtest.Input(`2: x`),
			want:    true,
		},
		"int does not match a quoted integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   stringtest.Input(`"2": x`),
			want:    false,
		},
		"int does not match a str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   stringtest.Input(`!!str 2: x`),
			want:    false,
		},
		"int does not match an explicit str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input: stringtest.Input(`
				? !!str 2
				: x
			`),
			want: false,
		},
		"int does not match an anchored str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2),
			input:   stringtest.Input(`&k !!str 2: x`),
			want:    false,
		},
		"float does not match a str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, 2.0),
			input:   stringtest.Input(`!!str 2: x`),
			want:    false,
		},
		"string matches a str-tagged integer key": {
			matcher: matcher.Content(twoKeyPath, "2"),
			input:   stringtest.Input(`!!str 2: x`),
			want:    true,
		},
		"int does not match a verbatim str-tagged integer": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: !<tag:yaml.org,2002:str> 2`),
			want:    false,
		},
		"int does not match a block scalar": {
			matcher: matcher.Content(versionPath, 2),
			input: stringtest.Input(`
				version: |-
				  2
			`),
			want: false,
		},
		"int does not match a quoted exponent": {
			matcher: matcher.Content(versionPath, 1000),
			input:   stringtest.Input(`version: "1e3"`),
			want:    false,
		},
		"float does not match a quoted integer": {
			matcher: matcher.Content(versionPath, 2.0),
			input:   stringtest.Input(`version: '2'`),
			want:    false,
		},
		"float does not match a str-tagged integer": {
			matcher: matcher.Content(versionPath, 2.0),
			input:   stringtest.Input(`version: !!str 2`),
			want:    false,
		},
		"float matches a float-tagged quoted integer": {
			matcher: matcher.Content(versionPath, 2.0),
			input:   stringtest.Input(`version: !!float '2'`),
			want:    true,
		},
		"int matches a plain exponent spelling": {
			matcher: matcher.Content(versionPath, 1000),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"float NaN matches nan": {
			matcher: matcher.Content(versionPath, math.NaN()),
			input:   stringtest.Input(`version: .nan`),
			want:    true,
		},
		"float NaN matches NaN spelling": {
			matcher: matcher.Content(versionPath, math.NaN()),
			input:   stringtest.Input(`version: .NaN`),
			want:    true,
		},
		"float NaN does not match number": {
			matcher: matcher.Content(versionPath, math.NaN()),
			input:   stringtest.Input(`version: 1.5`),
			want:    false,
		},
		"float NaN does not match quoted nan": {
			matcher: matcher.Content(versionPath, math.NaN()),
			input:   stringtest.Input(`version: ".nan"`),
			want:    false,
		},
		"float does not match nan": {
			matcher: matcher.Content(versionPath, 1.5),
			input:   stringtest.Input(`version: .nan`),
			want:    false,
		},
		"float does not match plain inf": {
			matcher: matcher.Content(versionPath, math.Inf(1)),
			input:   stringtest.Input(`version: inf`),
			want:    false,
		},
		"float does not match Infinity": {
			matcher: matcher.Content(versionPath, math.Inf(1)),
			input:   stringtest.Input(`version: Infinity`),
			want:    false,
		},
		"float NaN does not match plain nan": {
			matcher: matcher.Content(versionPath, math.NaN()),
			input:   stringtest.Input(`version: nan`),
			want:    false,
		},
		"float does not match a hex float": {
			matcher: matcher.Content(versionPath, 0.25),
			input:   stringtest.Input(`version: 0x1p-2`),
			want:    false,
		},
		"int does not match a hex float": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: 0x1p0`),
			want:    false,
		},
		"float matches a signed plain exponent": {
			matcher: matcher.Content(versionPath, -1000.0),
			input:   stringtest.Input(`version: -1e3`),
			want:    true,
		},
		"float32 inf does not match an overflowing plain exponent": {
			// The decoder reads 1e39 as a string and narrows it to +Inf.
			matcher: matcher.Content(versionPath, float32(math.Inf(1))),
			input:   stringtest.Input(`version: 1e39`),
			want:    false,
		},
		"float32 inf does not match an overflowing float": {
			matcher: matcher.Content(versionPath, float32(math.Inf(1))),
			input:   stringtest.Input(`version: 4.0e+38`),
			want:    false,
		},
		"float32 negative inf does not match an overflowing float": {
			matcher: matcher.Content(versionPath, float32(math.Inf(-1))),
			input:   stringtest.Input(`version: -1.0e+39`),
			want:    false,
		},
		"float32 inf matches inf": {
			matcher: matcher.Content(versionPath, float32(math.Inf(1))),
			input:   stringtest.Input(`version: .inf`),
			want:    true,
		},
		"float32 negative inf matches negative inf": {
			matcher: matcher.Content(versionPath, float32(math.Inf(-1))),
			input:   stringtest.Input(`version: -.inf`),
			want:    true,
		},
		"empty string does not match null": {
			matcher: matcher.Content(kindPath, ""),
			input:   stringtest.Input(`kind:`),
			want:    false,
		},
		"false does not match null": {
			matcher: matcher.Content(enabledPath, false),
			input:   stringtest.Input(`enabled: null`),
			want:    false,
		},
		"large int does not match a rounded float": {
			matcher: matcher.Content(versionPath, int64(9007199254740993)),
			input:   stringtest.Input(`version: 9007199254740992.0`),
			want:    false,
		},
		"large int matches its float spelling": {
			matcher: matcher.Content(versionPath, int64(9007199254740992)),
			input:   stringtest.Input(`version: 9007199254740992.0`),
			want:    true,
		},
		"int matches integer": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"negative int does not match unsigned": {
			matcher: matcher.Content(versionPath, -1),
			input:   stringtest.Input(`version: 18446744073709551615`),
			want:    false,
		},
		"float matches a plain exponent": {
			matcher: matcher.Content(versionPath, 1000.0),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"float matches a plain exponent fraction": {
			matcher: matcher.Content(versionPath, 2.5),
			input:   stringtest.Input(`version: 25e-1`),
			want:    true,
		},
		"float matches a large plain exponent": {
			matcher: matcher.Content(versionPath, 1e19),
			input:   stringtest.Input(`version: 1e19`),
			want:    true,
		},
		"float does not match a quoted exponent": {
			matcher: matcher.Content(versionPath, 1000.0),
			input:   stringtest.Input(`version: "1e3"`),
			want:    false,
		},
		"float NaN does not match plain NaN": {
			matcher: matcher.Content(versionPath, math.NaN()),
			input:   stringtest.Input(`version: NaN`),
			want:    false,
		},
		"string matches plain inf": {
			matcher: matcher.Content(versionPath, "inf"),
			input:   stringtest.Input(`version: inf`),
			want:    true,
		},
		"string matches a plain exponent": {
			matcher: matcher.Content(versionPath, "1e3"),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"named int matches integer": {
			matcher: matcher.Content(versionPath, namedInt(1)),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"named int does not match other integer": {
			matcher: matcher.Content(versionPath, namedInt(1)),
			input:   stringtest.Input(`version: 2`),
			want:    false,
		},
		"named float matches float": {
			matcher: matcher.Content(versionPath, namedFloat(1.5)),
			input:   stringtest.Input(`version: 1.5`),
			want:    true,
		},
		"named float matches a plain exponent": {
			// The decoder reads 1e3 as a string, which go-yaml cannot set
			// a named float from.
			matcher: matcher.Content(versionPath, namedFloat(1000)),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"named float32 matches a plain exponent": {
			matcher: matcher.Content(versionPath, namedFloat32(1000)),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"named float matches a plain exponent fraction": {
			matcher: matcher.Content(versionPath, namedFloat(2.5)),
			input:   stringtest.Input(`version: 25e-1`),
			want:    true,
		},
		"named float does not match a quoted exponent": {
			matcher: matcher.Content(versionPath, namedFloat(1000)),
			input:   stringtest.Input(`version: "1e3"`),
			want:    false,
		},
		"duration matches plain": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: 5s`),
			want:    true,
		},
		"duration matches double-quoted": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: "5s"`),
			want:    true,
		},
		"duration matches single-quoted": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: '5s'`),
			want:    true,
		},
		"duration matches block scalar": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   "timeout: |-\n  5s\n",
			want:    true,
		},
		"duration matches str tag": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: !!str 5s`),
			want:    true,
		},
		"duration does not match other duration": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: "6s"`),
			want:    false,
		},
		"duration does not match an integer of nanoseconds": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: 5000000000`),
			want:    false,
		},
		"duration does not match plain float": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: 1.5e3`),
			want:    false,
		},
		"duration does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: 1e3`),
			want:    false,
		},
		"duration does not match negative plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: -1e3`),
			want:    false,
		},
		"duration does not match anchored plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: &a 2E+3`),
			want:    false,
		},
		"duration does not match overflowing plain exponent": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: 1e999`),
			want:    false,
		},
		"duration does not match overflowing plain float": {
			matcher: matcher.Content(timeoutPath, 5*time.Second),
			input:   stringtest.Input(`timeout: 1.5e999`),
			want:    false,
		},
		"bool match": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: true`),
			want:    true,
		},
		"bool no match": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: false`),
			want:    false,
		},
		"bool matches a capitalized spelling": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: True`),
			want:    true,
		},
		"bool matches a bool-tagged quoted scalar": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: !!bool "true"`),
			want:    true,
		},
		"named bool matches": {
			matcher: matcher.Content(enabledPath, namedBool(true)),
			input:   stringtest.Input(`enabled: true`),
			want:    true,
		},
		"bool does not match string": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: "true"`),
			want:    false,
		},
		"bool does not match a tag over no value": {
			matcher: matcher.Content(enabledPath, true),
			input:   "enabled: !!bool\n",
			want:    false,
		},
		"lowest int64 does not match negative infinity": {
			matcher: matcher.Content(versionPath, int64(math.MinInt64)),
			input:   stringtest.Input(`version: -.inf`),
			want:    false,
		},
		"highest int64 does not match a larger plain exponent": {
			matcher: matcher.Content(versionPath, int64(math.MaxInt64)),
			input:   stringtest.Input(`version: 1e19`),
			want:    false,
		},
		"lowest int64 does not match a smaller float": {
			matcher: matcher.Content(versionPath, int64(math.MinInt64)),
			input:   stringtest.Input(`version: -1.0e+19`),
			want:    false,
		},
		"highest uint64 does not match 2^64": {
			matcher: matcher.Content(versionPath, uint64(math.MaxUint64)),
			input:   stringtest.Input(`version: 18446744073709551616.0`),
			want:    false,
		},
		"self-decoding integer reads a float its own way": {
			matcher: matcher.Content(versionPath, millis(2000)),
			input:   stringtest.Input(`version: 2.0`),
			want:    true,
		},
		"self-decoding integer never matches a fraction": {
			matcher: matcher.Content(versionPath, millis(2500)),
			input:   stringtest.Input(`version: 2.5`),
			want:    false,
		},
		"value that does not decode": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: abc`),
			want:    false,
		},
		"mapping does not decode into scalar": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input: stringtest.Input(`
				kind:
				  name: Deployment
			`),
			want: false,
		},
		"sequence does not decode into scalar": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: [Deployment]`),
			want:    false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			got := match(t, tc.matcher, doc)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nested path match", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(metadataName, "my-app")
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			metadata:
			  name: my-app
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("duplicate key matches the value the decode keeps", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			kind: Pod
			kind: Service
		`), niceyaml.WithAllowDuplicateKeys(true))
		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 1)

		assert.True(t, match(t, matcher.Content(kindPath, "Service"), docs[0]))
		assert.False(t, match(t, matcher.Content(kindPath, "Pod"), docs[0]))
	})

	t.Run("node a decode hands its validator", func(t *testing.T) {
		t.Parallel()

		// The Node a validator gets decodes with the settings of its
		// source, so a decode of it reads an alias to an anchor of a
		// reference document. The path of a matcher resolves in the
		// document alone, so it cannot follow such an alias.
		refs := niceyaml.WithReferences(niceyaml.NewSourceFromString(stringtest.Input(`
			r: &r 2
		`)))

		tcs := map[string]struct {
			matcher matcher.Matcher
			err     error
			input   string
			want    bool
		}{
			"value beside an alias to a reference anchor matches": {
				matcher: matcher.Content(versionPath, 2),
				input: stringtest.Input(`
					other: *r
					version: 2
				`),
				want: true,
			},
			"alias to a reference anchor is an error": {
				matcher: matcher.Content(versionPath, 2),
				input:   `version: *r`,
				err:     paths.ErrAlias,
			},
			"tagged alias to a reference anchor is an error": {
				matcher: matcher.Content(versionPath, 2),
				input:   `version: !!int *r`,
				err:     paths.ErrAlias,
			},
			"alias that leads to a tagged alias to a reference anchor is an error": {
				matcher: matcher.Content(versionPath, 2),
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

				got, err := matchInDecode(t, tc.matcher, doc)
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("alias that does not resolve is an error", func(t *testing.T) {
		t.Parallel()

		// A tag on the alias changes nothing.
		tcs := map[string]struct {
			matcher matcher.Matcher
			input   string
		}{
			"alias without an anchor": {
				matcher: matcher.Content(kindPath, "Deployment"),
				input:   `kind: *missing`,
			},
			"tagged alias without an anchor": {
				matcher: matcher.Content(kindPath, "Deployment"),
				input:   `kind: !!str *missing`,
			},
			"tagged alias to itself": {
				matcher: matcher.Content(kindPath, "Deployment"),
				input:   `kind: &a !!str *a`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				ok, err := tc.matcher.Match(t.Context(), doc)
				require.ErrorIs(t, err, paths.ErrAlias)
				assert.False(t, ok)
			})
		}
	})

	t.Run("wildcard path is an error", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(paths.Current().Child("items").IndexAll(), "x")
		doc := yamltest.FirstDocument(t, stringtest.Input(`items: [x]`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrWildcard)
	})

	t.Run("alias bomb is an error", func(t *testing.T) {
		t.Parallel()

		// Each level lists the level below ten times, so a decode of the
		// alias key reads 10^7 scalars. Match refuses the document before
		// it decodes anything, as the schema validator does.
		input := yamltest.AliasLevels(7) + "kind:\n  ? *l7\n  : v\n"

		m := matcher.Content(kindPath, "Deployment")
		doc := yamltest.FirstDocument(t, input)

		ok, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
		assert.False(t, ok)

		// The aliases of the document are the cause, so the document is
		// at fault for the refusal.
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("alias bomb in a source with the limit off is read", func(t *testing.T) {
		t.Parallel()

		// The tag keeps the alias in the node at kind, so Match counts the
		// document before it reads the node, and the levels put it past
		// the limit.
		input := "name: &name Deployment\n" + yamltest.AliasLevels(4) + "kind: !!str *name\n"
		m := matcher.Content(kindPath, "Deployment")

		ok, err := m.Match(t.Context(), yamltest.FirstDocument(t, input))
		require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
		assert.False(t, ok)

		ok, err = m.Match(t.Context(), yamltest.FirstDocument(t, input, niceyaml.WithAliasLimit(false)))
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("scalar aliases written out as text", func(t *testing.T) {
		t.Parallel()

		// A type that decodes itself from text or from YAML bytes gets
		// the node written out, with a copy of the long scalar for each
		// alias. A plain string reads the list as a value, which shares
		// the scalar, so it does not match.
		input := "a: &a " + strings.Repeat("x", 2000) + "\n" +
			"kind: [" + strings.TrimSuffix(strings.Repeat("*a, ", 500), ", ") + "]\n"

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

func TestContent_WithAll(t *testing.T) {
	t.Parallel()

	t.Run("multiple conditions all match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(apiVersionPath, "apps/v1"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: apps/v1
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("multiple conditions partial match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(apiVersionPath, "apps/v1"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: v1
		`))

		got := match(t, m, doc)
		assert.False(t, got)
	})
}

func TestContent_ContextEnded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

	// A matcher whose context ended cannot decide, so it returns the error
	// rather than a match, and the registry stops at the document.
	ok, err := matcher.Content(kindPath, "Deployment").Match(ctx, doc)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
}

func TestContent_UnmarshalerError(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

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

func TestContent_UnparsableDuration(t *testing.T) {
	t.Parallel()

	// A string that time.ParseDuration rejects does not read as a
	// time.Duration, and the decode reports it with
	// niceyaml.ErrDecode, so the matcher answers no rather than
	// returning the error. A quoted float is such a string.
	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
	}{
		"plain words": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: 5 minutes`),
		},
		"quoted exponent": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: "1e3"`),
		},
		"str tagged exponent": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: !!str 1e3`),
		},
		"plain inf": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: inf`),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			ok, err := tc.matcher.Match(t.Context(), doc)
			require.NoError(t, err)
			assert.False(t, ok)
		})
	}
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
			matcher: matcher.Content(versionPath, evenInt(2)),
			input:   `version: 2`,
			want:    true,
		},
		"valid value does not match another": {
			matcher: matcher.Content(versionPath, evenInt(2)),
			input:   `version: 4`,
		},
		"invalid value is an error": {
			matcher: matcher.Content(versionPath, evenInt(2)),
			input:   `version: 3`,
			err:     errOdd,
		},
		"invalid value is an error for a want that equals it": {
			matcher: matcher.Content(versionPath, evenInt(3)),
			input:   `version: 3`,
			err:     errOdd,
		},
		"value the decoder rejects does not match": {
			matcher: matcher.Content(versionPath, evenInt(2)),
			input:   `version: three`,
		},
		"null does not match": {
			matcher: matcher.Content(versionPath, evenInt(2)),
			input:   `version: null`,
		},
		"named float validates a plain exponent": {
			matcher: matcher.Content(versionPath, share(1000)),
			input:   `version: 1e3`,
			want:    true,
		},
		"named float rejects a negative plain exponent": {
			matcher: matcher.Content(versionPath, share(1000)),
			input:   `version: -1e3`,
			err:     errNegative,
		},
		"self-decoding value validates its own decode": {
			matcher: matcher.Content(versionPath, checkedString("v:good")),
			input:   `version: good`,
			want:    true,
		},
		"self-decoding value rejects its own decode": {
			matcher: matcher.Content(versionPath, checkedString("v:bad")),
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
