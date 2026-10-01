package matcher_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
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
		"uncomparable dynamic type does not match": {
			// T is any, so the compared values may hold a map, which ==
			// cannot compare. The matcher declines rather than panics.
			matcher: matcher.Content[any](kindPath, map[string]any{"a": uint64(1)}),
			input:   stringtest.Input("kind:\n  a: 1"),
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
		"any int does not match a quoted integer behind a tagged alias": {
			matcher: matcher.Content[any](versionPath, 2),
			input: stringtest.Input(`
				v: &v "2"
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
		"any int does not match a str-tagged integer key": {
			matcher: matcher.Content[any](twoKeyPath, 2),
			input:   stringtest.Input(`!!str 2: x`),
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
		"any NaN matches NaN spelling": {
			matcher: matcher.Content[any](versionPath, math.NaN()),
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
		"nil matches null": {
			matcher: matcher.Content[any](enabledPath, nil),
			input:   stringtest.Input(`enabled: ~`),
			want:    true,
		},
		"nil pointer matches null": {
			matcher: matcher.Content[*string](enabledPath, nil),
			input:   stringtest.Input(`enabled: null`),
			want:    true,
		},
		"nil pointer does not match a value": {
			matcher: matcher.Content[*string](kindPath, nil),
			input:   stringtest.Input(`kind: Deployment`),
			want:    false,
		},
		"string pointer matches its pointee": {
			matcher: matcher.Content(kindPath, new("Deployment")),
			input:   stringtest.Input(`kind: Deployment`),
			want:    true,
		},
		"string pointer does not match other string": {
			matcher: matcher.Content(kindPath, new("Deployment")),
			input:   stringtest.Input(`kind: Service`),
			want:    false,
		},
		"string pointer does not match null": {
			matcher: matcher.Content(kindPath, new("")),
			input:   stringtest.Input(`kind: null`),
			want:    false,
		},
		"string pointer matches float text as written": {
			matcher: matcher.Content(versionPath, new("1.10")),
			input:   stringtest.Input(`version: 1.10`),
			want:    true,
		},
		"bool pointer matches its pointee": {
			matcher: matcher.Content(enabledPath, new(true)),
			input:   stringtest.Input(`enabled: true`),
			want:    true,
		},
		"int pointer does not match a fraction": {
			matcher: matcher.Content(versionPath, new(1)),
			input:   stringtest.Input(`version: 1.5`),
			want:    false,
		},
		"int pointer does not match a quoted integer": {
			matcher: matcher.Content(versionPath, new(2)),
			input:   stringtest.Input(`version: "2"`),
			want:    false,
		},
		"any string pointer matches its pointee": {
			matcher: matcher.Content[any](kindPath, new("Deployment")),
			input:   stringtest.Input(`kind: Deployment`),
			want:    true,
		},
		"any string pointer does not match other string": {
			matcher: matcher.Content[any](kindPath, new("Deployment")),
			input:   stringtest.Input(`kind: Service`),
			want:    false,
		},
		"any int pointer matches its pointee": {
			matcher: matcher.Content[any](versionPath, new(2)),
			input:   stringtest.Input(`version: 2`),
			want:    true,
		},
		"any int pointer does not match a quoted integer": {
			matcher: matcher.Content[any](versionPath, new(2)),
			input:   stringtest.Input(`version: "2"`),
			want:    false,
		},
		"any nil pointer matches null": {
			matcher: matcher.Content[any](kindPath, (*string)(nil)),
			input:   stringtest.Input(`kind: null`),
			want:    true,
		},
		"any nil pointer does not match a value": {
			matcher: matcher.Content[any](kindPath, (*string)(nil)),
			input:   stringtest.Input(`kind: Deployment`),
			want:    false,
		},
		"any large int does not match a rounded float": {
			matcher: matcher.Content[any](versionPath, int64(9007199254740993)),
			input:   stringtest.Input(`version: 9007199254740992.0`),
			want:    false,
		},
		"any large int matches its float spelling": {
			matcher: matcher.Content[any](versionPath, int64(9007199254740992)),
			input:   stringtest.Input(`version: 9007199254740992.0`),
			want:    true,
		},
		"nil does not match a value": {
			matcher: matcher.Content[any](enabledPath, nil),
			input:   stringtest.Input(`enabled: false`),
			want:    false,
		},
		"any int matches integer": {
			matcher: matcher.Content[any](versionPath, 1),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"any int matches float spelling": {
			matcher: matcher.Content[any](versionPath, 1),
			input:   stringtest.Input(`version: 1.0`),
			want:    true,
		},
		"any int does not match other integer": {
			matcher: matcher.Content[any](versionPath, 1),
			input:   stringtest.Input(`version: 2`),
			want:    false,
		},
		"any negative int does not match unsigned": {
			matcher: matcher.Content[any](versionPath, -1),
			input:   stringtest.Input(`version: 18446744073709551615`),
			want:    false,
		},
		"any float matches a plain exponent": {
			matcher: matcher.Content[any](versionPath, 1000.0),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"any int matches a plain exponent": {
			matcher: matcher.Content[any](versionPath, 1000),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"any float matches a plain exponent fraction": {
			matcher: matcher.Content[any](versionPath, 2.5),
			input:   stringtest.Input(`version: 25e-1`),
			want:    true,
		},
		"any float matches a large plain exponent": {
			matcher: matcher.Content[any](versionPath, 1e19),
			input:   stringtest.Input(`version: 1e19`),
			want:    true,
		},
		"any int does not match a plain exponent fraction": {
			matcher: matcher.Content[any](versionPath, 2),
			input:   stringtest.Input(`version: 25e-1`),
			want:    false,
		},
		"any float does not match a quoted exponent": {
			matcher: matcher.Content[any](versionPath, 1000.0),
			input:   stringtest.Input(`version: "1e3"`),
			want:    false,
		},
		"any float does not match plain inf": {
			matcher: matcher.Content[any](versionPath, math.Inf(1)),
			input:   stringtest.Input(`version: inf`),
			want:    false,
		},
		"any float does not match Infinity": {
			matcher: matcher.Content[any](versionPath, math.Inf(1)),
			input:   stringtest.Input(`version: Infinity`),
			want:    false,
		},
		"any NaN does not match plain NaN": {
			matcher: matcher.Content[any](versionPath, math.NaN()),
			input:   stringtest.Input(`version: NaN`),
			want:    false,
		},
		"any float does not match a hex float": {
			matcher: matcher.Content[any](versionPath, 0.25),
			input:   stringtest.Input(`version: 0x1p-2`),
			want:    false,
		},
		"any string matches plain inf": {
			matcher: matcher.Content[any](versionPath, "inf"),
			input:   stringtest.Input(`version: inf`),
			want:    true,
		},
		"any string matches a plain exponent": {
			matcher: matcher.Content[any](versionPath, "1e3"),
			input:   stringtest.Input(`version: 1e3`),
			want:    true,
		},
		"any named int matches integer": {
			matcher: matcher.Content[any](versionPath, namedInt(1)),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"any named int does not match other integer": {
			matcher: matcher.Content[any](versionPath, namedInt(1)),
			input:   stringtest.Input(`version: 2`),
			want:    false,
		},
		"any named float matches float": {
			matcher: matcher.Content[any](versionPath, namedFloat(1.5)),
			input:   stringtest.Input(`version: 1.5`),
			want:    true,
		},
		"any duration matches integer": {
			matcher: matcher.Content[any](versionPath, time.Duration(80)),
			input:   stringtest.Input(`version: 80`),
			want:    true,
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
		"pointer duration matches quoted": {
			matcher: matcher.Content(timeoutPath, new(5*time.Second)),
			input:   stringtest.Input(`timeout: "5s"`),
			want:    true,
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
		"pointer duration does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, new(5*time.Second)),
			input:   stringtest.Input(`timeout: 1e3`),
			want:    false,
		},
		"array duration element matches duration text": {
			matcher: matcher.Content(timeoutPath, [1]time.Duration{5 * time.Second}),
			input:   stringtest.Input(`timeout: [5s]`),
			want:    true,
		},
		"array duration element does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, [1]time.Duration{5 * time.Second}),
			input:   stringtest.Input(`timeout: [1e3]`),
			want:    false,
		},
		"array duration elements do not match plain exponent before float": {
			matcher: matcher.Content(timeoutPath, [2]time.Duration{5 * time.Second, 5 * time.Second}),
			input:   stringtest.Input(`timeout: [1e3, 1.5e3]`),
			want:    false,
		},
		"array duration elements do not match plain exponent after float": {
			matcher: matcher.Content(timeoutPath, [2]time.Duration{5 * time.Second, 5 * time.Second}),
			input:   stringtest.Input(`timeout: [1.5e3, 1e3]`),
			want:    false,
		},
		"array duration does not match plain exponent past its end": {
			matcher: matcher.Content(timeoutPath, [1]time.Duration{5 * time.Second}),
			input:   stringtest.Input(`timeout: [5s, 1e3]`),
			want:    false,
		},
		"array duration element does not match aliased plain exponent": {
			matcher: matcher.Content(timeoutPath, [1]time.Duration{5 * time.Second}),
			input: stringtest.Input(`
				a: &a 1e3
				timeout: [*a]
			`),
			want: false,
		},
		"struct duration field does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, durationField{5 * time.Second}),
			input:   stringtest.Input(`timeout: {t: 1e3}`),
			want:    false,
		},
		"struct duration field does not match merged plain exponent": {
			matcher: matcher.Content(timeoutPath, durationField{5 * time.Second}),
			input:   stringtest.Input(`timeout: {<<: {t: 1e3}}`),
			want:    false,
		},
		"struct duration field matches its own entry before a dropped merge": {
			matcher: matcher.Content(timeoutPath, durationField{5 * time.Second}),
			input:   stringtest.Input(`timeout: {t: 5s, <<: {7: x, t: 1e3}}`),
			want:    true,
		},
		"inline struct duration field does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, inlineDuration{durationField{5 * time.Second}}),
			input:   stringtest.Input(`timeout: {t: 1e3}`),
			want:    false,
		},
		"array struct pointer duration field does not match plain exponent": {
			matcher: matcher.Content(timeoutPath, [1]*durationField{{5 * time.Second}}),
			input:   stringtest.Input(`timeout: [{t: 1e3}]`),
			want:    false,
		},
		"struct with an inline alias field ignores a plain exponent beside it": {
			matcher: matcher.Content(timeoutPath, aliasInlineDuration{K: "x"}),
			input:   stringtest.Input(`timeout: {t: 1e3, k: x}`),
			want:    true,
		},
		"struct that inlines itself does not match": {
			matcher: matcher.Content(timeoutPath, selfInline{T: 5 * time.Second}),
			input:   stringtest.Input(`timeout: {t: 5s}`),
			want:    false,
		},
		"time matches UTC timestamp": {
			matcher: matcher.Content(createdPath, time.Date(2001, 12, 15, 2, 59, 43, 0, time.UTC)),
			input:   stringtest.Input(`created: 2001-12-15T02:59:43Z`),
			want:    true,
		},
		"time matches offset timestamp in same offset": {
			matcher: matcher.Content(createdPath, time.Date(2001, 12, 14, 21, 59, 43, 0, time.FixedZone("", -5*3600))),
			input:   stringtest.Input(`created: 2001-12-14T21:59:43-05:00`),
			want:    true,
		},
		"time matches offset timestamp in UTC": {
			matcher: matcher.Content(createdPath, time.Date(2001, 12, 15, 2, 59, 43, 0, time.UTC)),
			input:   stringtest.Input(`created: 2001-12-14T21:59:43-05:00`),
			want:    true,
		},
		"time does not match other instant": {
			matcher: matcher.Content(createdPath, time.Date(2001, 12, 15, 3, 0, 0, 0, time.UTC)),
			input:   stringtest.Input(`created: 2001-12-14T21:59:43-05:00`),
			want:    false,
		},
		"pointer time matches offset timestamp": {
			matcher: matcher.Content(createdPath, new(time.Date(2001, 12, 15, 2, 59, 43, 0, time.UTC))),
			input:   stringtest.Input(`created: 2001-12-14T21:59:43-05:00`),
			want:    true,
		},
		"zero time matches zero date": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: 0001-01-01`),
			want:    true,
		},
		"zero time matches zero timestamp": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: 0001-01-01T00:00:00Z`),
			want:    true,
		},
		"zero time matches tagged zero date": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp 0001-01-01`),
			want:    true,
		},
		"zero time matches tagged block zero date": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				created: !!timestamp |-
				  0001-01-01
			`),
			want: true,
		},
		"zero time does not match word": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: hello`),
			want:    false,
		},
		"zero time does not match quoted string": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: "x"`),
			want:    false,
		},
		"zero time does not match empty string": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: ""`),
			want:    false,
		},
		"zero time does not match plain float": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: 1e3`),
			want:    false,
		},
		"zero time does not match tagged word": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp hello`),
			want:    false,
		},
		"zero time does not match tagged integer": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp 5`),
			want:    false,
		},
		"zero time does not match date under int tag": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp !!int 2001-12-14`),
			want:    false,
		},
		"zero time does not match date under float tag": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp !!float 2001-12-14`),
			want:    false,
		},
		"time matches date under str tag": {
			matcher: matcher.Content(createdPath, time.Date(2001, 12, 14, 0, 0, 0, 0, time.UTC)),
			input:   stringtest.Input(`created: !!timestamp !!str 2001-12-14`),
			want:    true,
		},
		"zero time does not match aliased tagged word": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a !!timestamp hello
				created: *a
			`),
			want: false,
		},
		"time matches tagged alias to date": {
			matcher: matcher.Content(createdPath, time.Date(2001, 12, 14, 0, 0, 0, 0, time.UTC)),
			input: stringtest.Input(`
				x: &a 2001-12-14
				created: !!timestamp *a
			`),
			want: true,
		},
		"zero time does not match tagged alias to word": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a hello
				created: !!timestamp *a
			`),
			want: false,
		},
		"zero time matches tagged alias to zero date": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a 0001-01-01
				created: !!timestamp *a
			`),
			want: true,
		},
		"zero time matches tagged alias to the nearest anchor before it": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a hello
				y: &a 0001-01-01
				created: !!timestamp *a
			`),
			want: true,
		},
		"zero time does not match tagged alias to a word before a later anchor": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a hello
				created: !!timestamp *a
				y: &a 0001-01-01
			`),
			want: false,
		},
		"zero time matches tagged alias through a second tagged alias": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a 0001-01-01
				y: &b !!timestamp *a
				created: !!timestamp *b
			`),
			want: true,
		},
		"zero time compares tagged alias through a second alias to word as the decode": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a hello
				y: &b !!timestamp *a
				created: !!timestamp *b
			`),
			want: true,
		},
		"zero time does not match alias to tagged alias to word": {
			matcher: matcher.Content(createdPath, time.Time{}),
			input: stringtest.Input(`
				x: &a hello
				y: &b !!timestamp *a
				created: *b
			`),
			want: false,
		},
		"pointer zero time does not match word": {
			matcher: matcher.Content(createdPath, new(time.Time)),
			input:   stringtest.Input(`created: hello`),
			want:    false,
		},
		"any zero time does not match tagged word": {
			matcher: matcher.Content[any](createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp hello`),
			want:    false,
		},
		"any zero time does not match date under int tag": {
			matcher: matcher.Content[any](createdPath, time.Time{}),
			input:   stringtest.Input(`created: !!timestamp !!int 2001-12-14`),
			want:    false,
		},
		"array zero time element does not match word": {
			matcher: matcher.Content(createdPath, [1]time.Time{}),
			input:   stringtest.Input(`created: [hello]`),
			want:    false,
		},
		"array time element matches tagged alias to date": {
			matcher: matcher.Content(createdPath, [1]time.Time{time.Date(2001, 12, 14, 0, 0, 0, 0, time.UTC)}),
			input: stringtest.Input(`
				x: &a 2001-12-14
				created: [!!timestamp *a]
			`),
			want: true,
		},
		"any string does not match number": {
			matcher: matcher.Content[any](versionPath, "1"),
			input:   stringtest.Input(`version: 1`),
			want:    false,
		},
		"bool match": {
			matcher: matcher.Content(enabledPath, true),
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
		"array matches equal elements": {
			matcher: matcher.Content(versionPath, [2]int{1, 2}),
			input:   stringtest.Input(`version: [1, 2]`),
			want:    true,
		},
		"array matches a shorter sequence with zero elements past its end": {
			matcher: matcher.Content(versionPath, [2]int{1, 0}),
			input:   stringtest.Input(`version: [1]`),
			want:    true,
		},
		"array int element does not match a fraction": {
			matcher: matcher.Content(versionPath, [1]int{2}),
			input:   stringtest.Input(`version: [2.5]`),
			want:    false,
		},
		"array int element does not match a quoted integer": {
			matcher: matcher.Content(versionPath, [1]int{1}),
			input:   stringtest.Input(`version: ["1"]`),
			want:    false,
		},
		"array float element does not match a quoted number": {
			matcher: matcher.Content(versionPath, [1]float64{2}),
			input:   stringtest.Input(`version: ["2"]`),
			want:    false,
		},
		"array float element does not match a Go-only float": {
			matcher: matcher.Content(versionPath, [1]float64{0.25}),
			input:   stringtest.Input(`version: [0x1p-2]`),
			want:    false,
		},
		"array int64 element does not match an overflowing float": {
			matcher: matcher.Content(versionPath, [1]int64{math.MaxInt64}),
			input:   stringtest.Input(`version: [1e19]`),
			want:    false,
		},
		"array float32 inf element does not match an overflowing float": {
			matcher: matcher.Content(versionPath, [1]float32{float32(math.Inf(1))}),
			input:   stringtest.Input(`version: [4.0e+38]`),
			want:    false,
		},
		"array string element matches float text as written": {
			matcher: matcher.Content(versionPath, [1]string{"1.10"}),
			input:   stringtest.Input(`version: [1.10]`),
			want:    true,
		},
		"array string element does not match respelled float": {
			matcher: matcher.Content(versionPath, [1]string{"1.1"}),
			input:   stringtest.Input(`version: [1.10]`),
			want:    false,
		},
		"array NaN element matches .nan": {
			matcher: matcher.Content(versionPath, [1]float64{math.NaN()}),
			input:   stringtest.Input(`version: [.nan]`),
			want:    true,
		},
		"array int element does not match null": {
			matcher: matcher.Content(versionPath, [1]int{0}),
			input:   stringtest.Input(`version: [null]`),
			want:    false,
		},
		"array nil pointer element matches null": {
			matcher: matcher.Content(versionPath, [1]*int{nil}),
			input:   stringtest.Input(`version: [null]`),
			want:    true,
		},
		"array time element matches offset timestamp": {
			matcher: matcher.Content(createdPath, [1]time.Time{time.Date(2001, 12, 15, 2, 59, 43, 0, time.UTC)}),
			input:   stringtest.Input(`created: [2001-12-14T21:59:43-05:00]`),
			want:    true,
		},
		"struct matches equal fields": {
			matcher: matcher.Content(versionPath, intText{2, "x"}),
			input:   stringtest.Input(`version: {i: 2, s: x}`),
			want:    true,
		},
		"struct matches a missing field as zero": {
			matcher: matcher.Content(versionPath, intText{2, ""}),
			input:   stringtest.Input(`version: {i: 2}`),
			want:    true,
		},
		"struct matches fields a merge key brings in": {
			matcher: matcher.Content(versionPath, intText{2, "x"}),
			input:   stringtest.Input(`version: {<<: {i: 2}, s: x}`),
			want:    true,
		},
		"struct int field does not match a fraction": {
			matcher: matcher.Content(versionPath, intText{2, "x"}),
			input:   stringtest.Input(`version: {i: 2.5, s: x}`),
			want:    false,
		},
		"struct int field does not match a quoted integer": {
			matcher: matcher.Content(versionPath, intText{2, "x"}),
			input:   stringtest.Input(`version: {i: "2", s: x}`),
			want:    false,
		},
		"struct string field matches float text as written": {
			matcher: matcher.Content(versionPath, intText{1, "1.10"}),
			input:   stringtest.Input(`version: {i: 1, s: 1.10}`),
			want:    true,
		},
		"nested struct int field does not match null": {
			matcher: matcher.Content(versionPath, nestedIntText{}),
			input:   stringtest.Input(`version: {a: {i: null}}`),
			want:    false,
		},
		"struct pointer field matches its pointee": {
			matcher: matcher.Content(versionPath, plainField{K: "x", N: new(2)}),
			input:   stringtest.Input(`version: {k: x, n: 2}`),
			want:    true,
		},
		"struct nil pointer field matches null": {
			matcher: matcher.Content(versionPath, plainField{K: "x"}),
			input:   stringtest.Input(`version: {k: x, n: null}`),
			want:    true,
		},
		"inline struct int field does not match a fraction": {
			matcher: matcher.Content(versionPath, inlineIntText{Inline: intText{I: 2}, S: "x"}),
			input:   stringtest.Input(`version: {i: 2.5, s: x}`),
			want:    false,
		},
		"inline struct matches with a shadowed field zero": {
			matcher: matcher.Content(versionPath, inlineIntText{Inline: intText{I: 2}, S: "x"}),
			input:   stringtest.Input(`version: {i: 2, s: x}`),
			want:    true,
		},
		"inline struct shadowed field does not match the entry of its parent": {
			matcher: matcher.Content(versionPath, inlineIntText{Inline: intText{I: 2, S: "x"}, S: "x"}),
			input:   stringtest.Input(`version: {i: 2, s: x}`),
			want:    false,
		},
		"struct field does not match a mapping with a number key": {
			matcher: matcher.Content(versionPath, intText{S: "1.10"}),
			input:   stringtest.Input(`version: {s: 1.10, 7: x}`),
			want:    false,
		},
		"struct zero fields match a mapping with a number key": {
			matcher: matcher.Content(versionPath, intText{}),
			input:   stringtest.Input(`version: {i: 2.5, 7: x}`),
			want:    true,
		},
		"struct field reads a mapping whose number key is quoted": {
			matcher: matcher.Content(versionPath, intText{S: "1.10"}),
			input:   stringtest.Input(`version: {s: 1.10, "7": x}`),
			want:    true,
		},
		"struct field named for a number key does not match it": {
			matcher: matcher.Content(versionPath, scalarKeys{One: "1.10"}),
			input:   stringtest.Input(`version: {1: 1.10}`),
			want:    false,
		},
		"struct field named for a bool key does not match it": {
			matcher: matcher.Content(versionPath, scalarKeys{True: "2"}),
			input:   stringtest.Input(`version: {true: 2}`),
			want:    false,
		},
		"struct field named for a number key reads it under a str tag": {
			matcher: matcher.Content(versionPath, scalarKeys{Sixteen: "x"}),
			input:   stringtest.Input(`version: {!!str 0x10: x}`),
			want:    true,
		},
		"struct field named for a number key does not read it untagged": {
			matcher: matcher.Content(versionPath, scalarKeys{Sixteen: "x"}),
			input:   stringtest.Input(`version: {0x10: x}`),
			want:    false,
		},
		"struct field does not match a merged mapping with a number key": {
			matcher: matcher.Content(versionPath, intText{S: "1.10"}),
			input:   stringtest.Input(`version: {<<: {1: x, s: 1.10}}`),
			want:    false,
		},
		"struct matches its own entries beside a merged mapping with a number key": {
			matcher: matcher.Content(versionPath, intText{2, "x"}),
			input:   stringtest.Input(`version: {<<: {1: x, i: 3}, i: 2, s: x}`),
			want:    true,
		},
		"inline struct zero fields match a mapping with a number key": {
			matcher: matcher.Content(versionPath, inlineIntText{}),
			input:   stringtest.Input(`version: {i: 2.5, 7: x}`),
			want:    true,
		},
		"struct matches its own entry before a merged mapping with a number key": {
			matcher: matcher.Content(versionPath, intText{I: 2}),
			input:   stringtest.Input(`version: {i: 2, <<: {7: x, i: 3.5}}`),
			want:    true,
		},
		"struct int field does not match its own null before a merged mapping with a number key": {
			matcher: matcher.Content(versionPath, intText{}),
			input:   stringtest.Input(`version: {i: null, <<: {7: x, i: 3.5}}`),
			want:    false,
		},
		"struct nil pointer field matches its own null before a merged mapping with a number key": {
			matcher: matcher.Content(versionPath, plainField{K: "x"}),
			input:   stringtest.Input(`version: {k: x, n: null, <<: {7: x, n: 3}}`),
			want:    true,
		},
		"struct field before a dropped merged mapping compares the decode": {
			matcher: matcher.Content(versionPath, intText{I: 2}),
			input:   stringtest.Input(`version: {i: 2.5, <<: {7: x, i: 3}}`),
			want:    true,
		},
		"struct string field does not match a null under a key a path cannot name": {
			matcher: matcher.Content(versionPath, scalarKeys{}),
			input:   stringtest.Input(`version: {!!str 0x10: null}`),
			want:    false,
		},
		"struct field under a key a path cannot name compares the decode": {
			matcher: matcher.Content(versionPath, scalarKeys{Sixteen: "1.1"}),
			input:   stringtest.Input(`version: {!!str 0x10: 1.10}`),
			want:    true,
		},
		"struct interface field holding a mapping matches nothing": {
			matcher: matcher.Content(versionPath, anyField{F: map[string]any{"a": uint64(1)}}),
			input:   stringtest.Input(`version: {!!str 0x10: {a: 1}}`),
			want:    false,
		},
		"struct with an inline alias field compares the decode beside a fraction": {
			matcher: matcher.Content(versionPath, aliasInline{K: "x"}),
			input:   stringtest.Input(`version: {i: 2.5, k: x}`),
			want:    true,
		},
		"struct with an inline alias field compares the decode beside float text": {
			matcher: matcher.Content(versionPath, aliasInline{K: "x"}),
			input:   stringtest.Input(`version: {s: 1.10, k: x}`),
			want:    true,
		},
		"struct with an inline alias field compares its own field as the decode": {
			matcher: matcher.Content(versionPath, aliasInline{K: "1.1"}),
			input:   stringtest.Input(`version: {k: 1.10}`),
			want:    true,
		},
		"struct with an inline alias field does not match a different decode": {
			matcher: matcher.Content(versionPath, aliasInline{K: "y"}),
			input:   stringtest.Input(`version: {i: 2, k: x}`),
			want:    false,
		},
		"struct with an omitempty inline alias field ignores a merged entry": {
			matcher: matcher.Content(versionPath, aliasInlineOmit{}),
			input:   stringtest.Input(`version: {<<: {k: 1.10}}`),
			want:    true,
		},
		"struct with an omitempty inline alias field ignores a merged fraction": {
			matcher: matcher.Content(versionPath, aliasInlineOmit{K: "x"}),
			input:   stringtest.Input(`version: {<<: {i: 2.5}, k: x}`),
			want:    true,
		},
		"array of structs with an inline alias field compares the decode": {
			matcher: matcher.Content(versionPath, [1]aliasInline{{K: "x"}}),
			input:   stringtest.Input(`version: [{i: 2.5, k: x}]`),
			want:    true,
		},
		"inline struct with an inline alias field compares the decode": {
			matcher: matcher.Content(versionPath, aliasInlineParent{S: "1.10"}),
			input:   stringtest.Input(`version: {i: 2.5, s: 1.10}`),
			want:    true,
		},
		"map item matches the first entry of a mapping": {
			matcher: matcher.Content(versionPath, yaml.MapItem{Key: "key", Value: "x"}),
			input:   stringtest.Input(`version: {key: x, value: null}`),
			want:    true,
		},
		"map item does not match a different first entry": {
			matcher: matcher.Content(versionPath, yaml.MapItem{Key: "key", Value: "y"}),
			input:   stringtest.Input(`version: {key: x, value: null}`),
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

		// The Node a validator gets decodes with the go-yaml options of the
		// decode that runs the validator, as a registry used as a validator
		// sees it.
		ordered := niceyaml.WithYAMLDecodeOptions(yaml.UseOrderedMap())
		refs := niceyaml.WithReferences([]byte(stringtest.Input(`
			r: &r 2
			m: &m {i: 2}
			n: &n null
			k: &k i
			o: &o {i: null}
			d: &d 2001-12-14
			z: &z 0001-01-01
			w: &w hello
		`)))

		// Match cannot see an unmarshaler that an option gives a type, so
		// it takes these types to decode by their kind.
		custom := niceyaml.WithYAMLDecodeOptions(
			yaml.CustomUnmarshaler(func(v *majorMinor, text []byte) error {
				_, err := fmt.Sscanf(string(text), "%d.%d", &v.Major, &v.Minor)
				if err != nil {
					return fmt.Errorf("read major.minor: %w", err)
				}

				return nil
			}),
			yaml.CustomUnmarshaler(func(v *intPair, text []byte) error {
				_, err := fmt.Sscanf(string(text), "%dx%d", &v[0], &v[1])
				if err != nil {
					return fmt.Errorf("read pair: %w", err)
				}

				return nil
			}),
			yaml.CustomUnmarshaler(func(v *time.Time, text []byte) error {
				if string(text) == "never" {
					return nil
				}

				t, err := time.Parse("01/02/2006", string(text))
				if err != nil {
					return fmt.Errorf("read date: %w", err)
				}

				*v = t

				return nil
			}),
		)

		date := time.Date(2001, 12, 14, 0, 0, 0, 0, time.UTC)

		tcs := map[string]struct {
			matcher matcher.Matcher
			err     error
			input   string
			opts    []niceyaml.DecodeOption
			want    bool
		}{
			"struct matches an ordered mapping": {
				matcher: matcher.Content(versionPath, intText{2, "x"}),
				input:   `version: {i: 2, s: x}`,
				opts:    []niceyaml.DecodeOption{ordered},
				want:    true,
			},
			"struct int field in an ordered mapping does not match a fraction": {
				matcher: matcher.Content(versionPath, intText{2, "x"}),
				input:   `version: {i: 2.5, s: x}`,
				opts:    []niceyaml.DecodeOption{ordered},
				want:    false,
			},
			"array of structs matches ordered mappings": {
				matcher: matcher.Content(versionPath, [1]intText{{2, "x"}}),
				input:   `version: [{i: 2, s: x}]`,
				opts:    []niceyaml.DecodeOption{ordered},
				want:    true,
			},
			"array element matches an alias to a reference anchor": {
				matcher: matcher.Content(versionPath, [1]int{2}),
				input:   `version: [*r]`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"array element does not match a different reference value": {
				matcher: matcher.Content(versionPath, [1]int{3}),
				input:   `version: [*r]`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    false,
			},
			"struct field matches an alias to a reference anchor": {
				matcher: matcher.Content(versionPath, intText{I: 2}),
				input:   `version: {i: *r}`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"struct matches a merged reference mapping": {
				matcher: matcher.Content(versionPath, intText{I: 2}),
				input:   `version: {<<: *m}`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"array int element does not match an alias to a reference null": {
				matcher: matcher.Content(versionPath, [1]int{0}),
				input:   `version: [*n]`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    false,
			},
			"array nil pointer element matches an alias to a reference null": {
				matcher: matcher.Content(versionPath, [1]*int{nil}),
				input:   `version: [*n]`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"struct int field does not match an alias to a reference null": {
				matcher: matcher.Content(versionPath, intText{}),
				input:   `version: {i: *n}`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    false,
			},
			"struct field under an alias key to a reference anchor compares the decode": {
				matcher: matcher.Content(versionPath, intText{I: 2}),
				input:   `version: {*k : 2.5}`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"struct int field does not match a null under an alias key to a reference anchor": {
				matcher: matcher.Content(versionPath, intText{}),
				input:   `version: {*k : null}`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    false,
			},
			"struct field compares a null inside a reference value as the decode": {
				matcher: matcher.Content(versionPath, nestedIntText{}),
				input:   `version: {a: *o}`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"struct an option reads from a scalar compares the decode": {
				matcher: matcher.Content(versionPath, majorMinor{Major: 1, Minor: 10}),
				input:   `version: 1.10`,
				opts:    []niceyaml.DecodeOption{custom},
				want:    true,
			},
			"struct an option reads from a scalar does not match a different decode": {
				matcher: matcher.Content(versionPath, majorMinor{Major: 1, Minor: 1}),
				input:   `version: 1.10`,
				opts:    []niceyaml.DecodeOption{custom},
				want:    false,
			},
			"array an option reads from a scalar compares the decode": {
				matcher: matcher.Content(versionPath, intPair{2, 3}),
				input:   `version: 2x3`,
				opts:    []niceyaml.DecodeOption{custom},
				want:    true,
			},
			"array an option reads from a scalar does not match a different decode": {
				matcher: matcher.Content(versionPath, intPair{3, 2}),
				input:   `version: 2x3`,
				opts:    []niceyaml.DecodeOption{custom},
				want:    false,
			},
			"alias to a reference anchor is an error": {
				matcher: matcher.Content(createdPath, date),
				input:   `created: *d`,
				opts:    []niceyaml.DecodeOption{refs},
				err:     paths.ErrAlias,
			},
			"tagged alias to a reference anchor is an error": {
				matcher: matcher.Content(createdPath, date),
				input:   `created: !!timestamp *d`,
				opts:    []niceyaml.DecodeOption{refs},
				err:     paths.ErrAlias,
			},
			"alias that leads to a tagged alias to a reference anchor is an error": {
				matcher: matcher.Content(createdPath, time.Time{}),
				input: stringtest.Input(`
					x: &x !!timestamp *z
					created: *x
				`),
				opts: []niceyaml.DecodeOption{refs},
				err:  paths.ErrAlias,
			},
			"array time element matches a tagged alias to a reference anchor": {
				matcher: matcher.Content(createdPath, [1]time.Time{date}),
				input:   `created: [!!timestamp *d]`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"struct time field matches a tagged alias to a reference anchor": {
				matcher: matcher.Content(createdPath, struct {
					A time.Time `yaml:"a"`
				}{A: date}),
				input: `created: {a: !!timestamp *d}`,
				opts:  []niceyaml.DecodeOption{refs},
				want:  true,
			},
			"array zero time element compares a tagged alias to a reference word as the decode": {
				matcher: matcher.Content(createdPath, [1]time.Time{}),
				input:   `created: [!!timestamp *w]`,
				opts:    []niceyaml.DecodeOption{refs},
				want:    true,
			},
			"time an option reads from other text compares the decode": {
				matcher: matcher.Content(createdPath, date),
				input:   `created: 12/14/2001`,
				opts:    []niceyaml.DecodeOption{custom},
				want:    true,
			},
			"zero time an option reads from other text does not match": {
				matcher: matcher.Content(createdPath, time.Time{}),
				input:   `created: never`,
				opts:    []niceyaml.DecodeOption{custom},
				want:    false,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				got, err := matchInDecode(t, tc.matcher, doc, tc.opts...)
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
				matcher: matcher.Content(createdPath, time.Time{}),
				input:   `created: !!timestamp *missing`,
			},
			"tagged alias to itself": {
				matcher: matcher.Content(createdPath, time.Time{}),
				input:   `created: &a !!timestamp *a`,
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

		m := matcher.Content(paths.Root().Child("items").IndexAll(), "x")
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
			m   matcher.Matcher
			err error
		}{
			"text unmarshaler": {
				m:   matcher.Content(kindPath, prefixedString("vx")),
				err: schema.ErrExcessiveAliasing,
			},
			"pointer to a text unmarshaler": {
				m:   matcher.Content(kindPath, new(prefixedString)),
				err: schema.ErrExcessiveAliasing,
			},
			"bytes unmarshaler": {
				m:   matcher.Content(kindPath, millis(1000)),
				err: schema.ErrExcessiveAliasing,
			},
			"struct field text unmarshaler": {
				m:   matcher.Content(kindPath, textField{}),
				err: schema.ErrExcessiveAliasing,
			},
			"array element text unmarshaler": {
				m:   matcher.Content(kindPath, [2]prefixedString{}),
				err: schema.ErrExcessiveAliasing,
			},
			"slice element behind a pointer": {
				m:   matcher.Content(kindPath, new(textSlice)),
				err: schema.ErrExcessiveAliasing,
			},
			"recursive type with a text field": {
				m:   matcher.Content(kindPath, textChain{}),
				err: schema.ErrExcessiveAliasing,
			},
			"unmarshaler that decodes into text unmarshalers": {
				m:   matcher.Content(kindPath, forwardedText("vx")),
				err: schema.ErrExcessiveAliasing,
			},
			"plain string": {
				m: matcher.Content(kindPath, "x"),
			},
			"struct of plain fields": {
				m: matcher.Content(kindPath, plainField{}),
			},
			"time": {
				m: matcher.Content(kindPath, time.Time{}),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, input)

				ok, err := tc.m.Match(t.Context(), doc)
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}

				assert.False(t, ok)
			})
		}
	})
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

// matchInDecode runs m on the Node that a decode of doc with opts hands
// its validator, and returns what m returns.
func matchInDecode(
	t *testing.T, m matcher.Matcher, doc *niceyaml.Node, opts ...niceyaml.DecodeOption,
) (bool, error) {
	t.Helper()

	var (
		got      bool
		matchErr error
	)

	validator := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		got, matchErr = m.Match(ctx, n)

		return nil
	})

	_, err := doc.Decode[any](t.Context(), append(opts, niceyaml.WithValidator(validator))...)
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

// textField, textSlice, and textChain reach a [prefixedString] through a
// field or an element, so a decode into them reads text too.
type (
	textField struct {
		K prefixedString
	}
	textSlice struct {
		S []prefixedString
	}
	textChain struct {
		Next *textChain
		K    prefixedString
	}
)

// intText is a struct want whose fields read the entries i and s.
type intText struct {
	I int
	S string
}

// durationField is a struct want whose field reads the entry t as a
// [time.Duration].
type durationField struct {
	T time.Duration
}

// inlineDuration reads the entries of a [durationField] inline.
type inlineDuration struct {
	Inline durationField `yaml:",inline"`
}

// aliasInlineDuration holds a [durationField] in an inline field with an
// alias option, which the decoder fills from an anchor and never from
// the entry t.
type aliasInlineDuration struct {
	Inline durationField `yaml:",inline,alias"`
	K      string
}

// selfInline inlines itself through a pointer, so the decoder reads the
// same mapping into it until it reaches its depth limit and rejects it.
type selfInline struct {
	Next *selfInline `yaml:",inline"`
	T    time.Duration
}

// inlineIntText reads the entries of an [intText] inline. Its own S
// shadows the S of the inline struct, which the decoder leaves zero.
type inlineIntText struct {
	Inline intText `yaml:",inline"`
	S      string
}

// nestedIntText is a struct want whose field reads the entry a as an
// [intText].
type nestedIntText struct {
	A intText
}

// aliasInline holds an inline field with an alias option. The decoder
// fills Base from the anchor that a `<<` key names with an alias, and
// from no entry of the mapping, so Base stays zero without such a key.
type aliasInline struct {
	Base intText `yaml:",inline,alias"`
	K    string
}

// aliasInlineOmit is an [aliasInline] whose inline field carries
// omitempty too, which makes the decoder read no `<<` key into K either.
type aliasInlineOmit struct {
	Base intText `yaml:",omitempty,inline,alias"`
	K    string
}

// aliasInlineParent reads the entries of an [aliasInline] inline, beside
// a field of its own.
type aliasInlineParent struct {
	Inline aliasInline `yaml:",inline"`
	S      string
}

// majorMinor is a struct and intPair an array with no method that
// decodes them. A test gives each a [yaml.CustomUnmarshaler] that reads
// it from a scalar.
type (
	majorMinor struct {
		Major int
		Minor int
	}
	intPair [2]int
)

// scalarKeys reads entries whose plain keys spell a number or a bool.
// The decoder reads such a key as a string only under a !!str tag.
type scalarKeys struct {
	One     string `yaml:"1"`
	True    string `yaml:"true"`
	Sixteen string `yaml:"16"`
}

// anyField reads the key !!str 0x10 into an interface, which holds a map
// when the value is a mapping. The document has no node at the path of
// the field, since the path spells the key 16.
type anyField struct {
	F any `yaml:"16"`
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

// plainField holds only fields that decode as plain values do.
type plainField struct {
	K string
	N *int
}

// namedInt and namedFloat are numeric types a caller names, which compare
// by value behind an interface as the predeclared types do.
type (
	namedInt   int
	namedFloat float64
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

// The error rejecting reports from its own decode.
var errRejecting = errors.New("value rejected itself")

// rejecting decodes itself and reports errRejecting, so the decoder
// returns the value's own error rather than a rejection of its own.
type rejecting struct{}

func (*rejecting) UnmarshalYAML([]byte) error {
	return errRejecting
}

func TestContent_UnmarshalerError(t *testing.T) {
	t.Parallel()

	// A value that rejects itself is not the decoder saying the value
	// does not read as T, so the matcher returns the error rather than
	// a no.
	m := matcher.Content(kindPath, rejecting{})
	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

	ok, err := m.Match(t.Context(), doc)
	require.ErrorIs(t, err, errRejecting)
	assert.False(t, ok)
}

func TestContent_UnparsableDuration(t *testing.T) {
	t.Parallel()

	// The decoder reports a string that time.ParseDuration rejects
	// without niceyaml.ErrDecodeRejected, so the matcher returns the
	// error rather than a no. A quoted float is such a string, in an
	// element or a field too.
	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		err     string
	}{
		"plain words": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: 5 minutes`),
			err:     "unknown unit",
		},
		"quoted exponent": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: "1e3"`),
			err:     "unknown unit",
		},
		"str tagged exponent": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: !!str 1e3`),
			err:     "unknown unit",
		},
		"plain inf": {
			matcher: matcher.Content(timeoutPath, time.Minute),
			input:   stringtest.Input(`timeout: inf`),
			err:     "invalid duration",
		},
		"array element quoted exponent": {
			matcher: matcher.Content(timeoutPath, [1]time.Duration{time.Minute}),
			input:   stringtest.Input(`timeout: ["1e3"]`),
			err:     "unknown unit",
		},
		"struct field quoted exponent": {
			matcher: matcher.Content(timeoutPath, durationField{time.Minute}),
			input:   stringtest.Input(`timeout: {t: "1e3"}`),
			err:     "unknown unit",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			ok, err := tc.matcher.Match(t.Context(), doc)
			require.ErrorContains(t, err, tc.err)
			require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
			assert.False(t, ok)
		})
	}
}

func TestContent_RefusedTargetType(t *testing.T) {
	t.Parallel()

	// The decoder refuses a struct with two fields of one name and
	// reports it without niceyaml.ErrDecodeRejected, so the matcher
	// returns the error rather than a no.
	type duplicated struct {
		A int `yaml:"a"`
		B int `yaml:"a"`
	}

	m := matcher.Content(paths.Root().Child("x"), duplicated{})
	doc := yamltest.FirstDocument(t, stringtest.Input(`x: {a: 1}`))

	ok, err := m.Match(t.Context(), doc)
	require.ErrorContains(t, err, "duplicated struct field name")
	require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
	assert.False(t, ok)
}
