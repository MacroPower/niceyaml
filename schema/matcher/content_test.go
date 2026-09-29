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

	t.Run("alias without an anchor is an error", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(kindPath, "Deployment")
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: *missing`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrAlias)
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
		var sb strings.Builder

		sb.WriteString("a:\n  - &l0 [x]\n")

		for level := 1; level <= 7; level++ {
			aliases := strings.Repeat(fmt.Sprintf("*l%d, ", level-1), 10)
			fmt.Fprintf(&sb, "  - &l%d [%s]\n", level, strings.TrimSuffix(aliases, ", "))
		}

		sb.WriteString("kind:\n  ? *l7\n  : v\n")

		m := matcher.Content(kindPath, "Deployment")
		doc := yamltest.FirstDocument(t, sb.String())

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
			"plain string": {
				m: matcher.Content(kindPath, "x"),
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

// namedInt and namedFloat are numeric types a caller names, which compare
// by value behind an interface as the predeclared types do.
type (
	namedInt   int
	namedFloat float64
)

// millis decodes itself from a number of seconds, which it holds as
// milliseconds.
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

	// The decoder reports a duration it cannot read without
	// niceyaml.ErrDecodeRejected, so the matcher returns the error rather
	// than a no.
	m := matcher.Content(paths.Root().Child("timeout"), time.Minute)
	doc := yamltest.FirstDocument(t, stringtest.Input(`timeout: 5 minutes`))

	ok, err := m.Match(t.Context(), doc)
	require.ErrorContains(t, err, "unknown unit")
	require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
	assert.False(t, ok)
}
