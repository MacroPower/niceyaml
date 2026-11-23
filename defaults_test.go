package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// defaultsCafe describes the café of the defaults tests.
type defaultsCafe struct {
	Labels   map[string]string          `yaml:"labels"`
	Owner    *defaultsPrinter           `yaml:"owner"`
	Spare    *defaultsPrinter           `yaml:"spare"`
	Printers map[string]defaultsPrinter `yaml:"printers"`
	Extra    any                        `yaml:"extra"`
	Currency string                     `yaml:"currency"`
	Stations []defaultsPrinter          `yaml:"stations"`
	Backups  []*defaultsPrinter         `yaml:"backups"`
	Receipts defaultsPrinter            `yaml:"receipts"`
	Kitchen  defaultsPrinter            `yaml:"kitchen"`
	Seats    int                        `yaml:"seats"`
}

// defaultsPrinter is a printer of the defaults tests.
type defaultsPrinter struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

var (
	// The rules of defaultsRules give the café and each printer their
	// defaults, and set the port of the kitchen printer and the target of
	// spare.
	defaultsRules = []niceyaml.TypeRule{
		niceyaml.TypeDefaults(func(c *defaultsCafe) {
			c.Seats = 20
			c.Currency = "SEK"
			c.Kitchen.Port = 9101
			c.Spare = &defaultsPrinter{Host: "spare", Port: 9200}
		}),
		niceyaml.TypeDefaults(func(p *defaultsPrinter) {
			p.Port = 9100
		}),
	}

	// The option defaultsDecoding gives a decode the rules of
	// defaultsRules.
	defaultsDecoding = niceyaml.OverrideTypeRules(defaultsRules...)

	// The counter defaultsLogLevelCalls counts each call of the method of
	// [defaultsLogLevel] that starts from a level that is not empty.
	defaultsLogLevelCalls atomic.Int64

	// The counter defaultsLabelsCalls counts each call of the method of
	// [defaultsLabels] that starts from a map.
	defaultsLabelsCalls atomic.Int64
)

// defaultedCafe returns the café that [defaultsDecoding] builds from its
// defaults alone, after each of edits changes it.
func defaultedCafe(edits ...func(*defaultsCafe)) defaultsCafe {
	c := defaultsCafe{
		Seats:    20,
		Currency: "SEK",
		Receipts: defaultsPrinter{Port: 9100},
		Kitchen:  defaultsPrinter{Port: 9101},
		Spare:    &defaultsPrinter{Host: "spare", Port: 9200},
	}

	for _, edit := range edits {
		edit(&c)
	}

	return c
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	got := niceyaml.Defaults[defaultsCafe](defaultsRules...)

	assert.Equal(t, defaultedCafe(), got)

	assert.Equal(t, defaultsPrinter{Port: 9100}, niceyaml.Defaults[defaultsPrinter](defaultsRules...))
	assert.Equal(t, defaultsCafe{}, niceyaml.Defaults[defaultsCafe]())
	assert.Nil(t, niceyaml.Defaults[*defaultsPrinter](defaultsRules...))

	// A later TypeDefaults for a type replaces an earlier one.
	later := niceyaml.Defaults[defaultsPrinter](append(defaultsRules,
		niceyaml.TypeDefaults(func(p *defaultsPrinter) { p.Host = "lp" }))...)
	assert.Equal(t, defaultsPrinter{Host: "lp"}, later)

	// A nil function removes the function of the type, and keeps those of
	// the other types.
	removed := niceyaml.Defaults[defaultsCafe](append(defaultsRules, niceyaml.TypeDefaults[defaultsCafe](nil))...)
	assert.Equal(t, defaultsCafe{Receipts: defaultsPrinter{Port: 9100}, Kitchen: defaultsPrinter{Port: 9100}}, removed)

	// The rules of other kinds do not change the defaults.
	assert.Equal(t, defaultsPrinter{Port: 9100}, niceyaml.Defaults[defaultsPrinter](append(defaultsRules,
		niceyaml.JSONUnmarshalers(true), niceyaml.CustomUnmarshaler[defaultsPrinter](nil), niceyaml.TypeRule{})...))

	// Each call builds new values, so a map the function sets belongs to
	// one value.
	labels := niceyaml.TypeDefaults(func(c *defaultsCafe) { c.Labels = map[string]string{"floor": "1"} })
	first := niceyaml.Defaults[defaultsCafe](labels)
	first.Labels["floor"] = "2"
	assert.Equal(t, "1", niceyaml.Defaults[defaultsCafe](labels).Labels["floor"])

	assert.PanicsWithValue(t, "boom", func() {
		niceyaml.Defaults[defaultsPrinter](niceyaml.TypeDefaults(func(*defaultsPrinter) { panic("boom") }))
	})
}

func TestDefaultsInto(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		target any
		want   any
		rules  []niceyaml.TypeRule
	}{
		"struct": {
			target: &defaultsCafe{},
			rules:  defaultsRules,
			want:   new(defaultedCafe()),
		},
		"replaces what the target held": {
			target: &defaultsPrinter{Host: "held", Port: 1},
			rules:  defaultsRules,
			want:   &defaultsPrinter{Port: 9100},
		},
		"without rules": {
			target: &defaultsPrinter{Host: "held", Port: 1},
			want:   &defaultsPrinter{},
		},
		"pointer to a pointer": {
			target: new(&defaultsPrinter{Port: 1}),
			rules:  defaultsRules,
			want:   new((*defaultsPrinter)(nil)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, niceyaml.DefaultsInto(tc.target, tc.rules...))
			assert.Equal(t, tc.want, tc.target)
		})
	}
}

func TestDefaultsInto_MatchesDefaults(t *testing.T) {
	t.Parallel()

	// A target built from a reflect.Type gets what Defaults returns.
	v := reflect.New(reflect.TypeFor[defaultsCafe]()).Interface()

	require.NoError(t, niceyaml.DefaultsInto(v, defaultsRules...))
	assert.Equal(t, new(niceyaml.Defaults[defaultsCafe](defaultsRules...)), v)
}

func TestDefaultsInto_Target(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		target any
	}{
		"nil":         {target: nil},
		"nil pointer": {target: (*defaultsCafe)(nil)},
		"not a pointer": {
			target: defaultsCafe{},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.DefaultsInto(tc.target, defaultsRules...)
			require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
			require.ErrorAs(t, err, new(*niceyaml.Error))
			assert.NotErrorAs(t, err, new(*niceyaml.SourceError))
			assert.False(t, niceyaml.IsInvalid(err))
		})
	}
}

func TestDefaultsInto_Panic(t *testing.T) {
	t.Parallel()

	var p defaultsPrinter

	assert.PanicsWithValue(t, "boom", func() {
		//nolint:errcheck // The call panics before it returns.
		_ = niceyaml.DefaultsInto(&p, niceyaml.TypeDefaults(func(*defaultsPrinter) { panic("boom") }))
	})
}

func TestSource_Defaults(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		source *niceyaml.Source
		opts   []niceyaml.DecodeOption
		want   defaultsCafe
	}{
		"rules of the Source": {
			source: niceyaml.NewSourceFromString("", niceyaml.WithTypeRules(defaultsRules...)),
			want:   defaultedCafe(),
		},
		"rules of the options": {
			source: niceyaml.NewSourceFromString(""),
			opts:   []niceyaml.DecodeOption{defaultsDecoding},
			want:   defaultedCafe(),
		},
		"options apply over the Source": {
			source: niceyaml.NewSourceFromString("", niceyaml.WithTypeRules(defaultsRules...)),
			opts: []niceyaml.DecodeOption{
				niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(c *defaultsCafe) { c.Seats = 4 })),
			},
			want: defaultsCafe{
				Seats:    4,
				Receipts: defaultsPrinter{Port: 9100},
				Kitchen:  defaultsPrinter{Port: 9100},
			},
		},
		"options remove a function of the Source": {
			source: niceyaml.NewSourceFromString("", niceyaml.WithTypeRules(defaultsRules...)),
			opts: []niceyaml.DecodeOption{
				niceyaml.OverrideTypeRules(niceyaml.TypeDefaults[defaultsPrinter](nil)),
			},
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Receipts.Port = 0
			}),
		},
		"nil Source": {
			opts: []niceyaml.DecodeOption{defaultsDecoding},
			want: defaultedCafe(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.source.Defaults[defaultsCafe](tc.opts...))

			v := &defaultsCafe{Seats: 1}
			require.NoError(t, tc.source.DefaultsInto(v, tc.opts...))
			assert.Equal(t, &tc.want, v)
		})
	}
}

func TestSource_Defaults_MatchesDecode(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("receipts: {host: r}\n", niceyaml.WithTypeRules(defaultsRules...))

	want, err := source.Decode[defaultsCafe](t.Context())
	require.NoError(t, err)

	// A caller that starts DecodeInto from the defaults of the Source gets
	// what Decode returns.
	start := source.Defaults[defaultsCafe]()
	require.NoError(t, source.DecodeInto(t.Context(), &start))
	assert.Equal(t, want, start)

	// A caller that holds its type as a runtime value and starts from
	// DefaultsInto gets the same.
	v := reflect.New(reflect.TypeFor[defaultsCafe]()).Interface()
	require.NoError(t, source.DefaultsInto(v))
	require.NoError(t, source.DecodeInto(t.Context(), v))
	assert.Equal(t, &want, v)

	// DefaultsInto reads no document, so its error comes back unbound.
	err = source.DefaultsInto(defaultsCafe{})
	require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)

	var sourceErr *niceyaml.SourceError

	assert.NotErrorAs(t, err, &sourceErr)
}

func TestNode_DecodeInto_DefaultsIntoStart(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"mapping":        {input: "receipts: {host: r}\n"},
		"null document":  {input: "~\n"},
		"empty document": {input: ""},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			want, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
			require.NoError(t, err)

			// A caller that starts from DefaultsInto gets what Decode
			// returns.
			v := reflect.New(reflect.TypeFor[defaultsCafe]()).Interface()
			require.NoError(t, niceyaml.DefaultsInto(v, defaultsRules...))
			require.NoError(t, doc.DecodeInto(t.Context(), v, defaultsDecoding))
			assert.Equal(t, &want, v)
		})
	}
}

func TestNode_DecodeInto_DefaultsThroughNilPointer(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input   string
		wantNil bool
	}{
		"mapping starts from the defaults": {
			input: "receipts: {host: r}\n",
		},
		"null leaves the pointer nil": {
			input:   "~\n",
			wantNil: true,
		},
		"null tag leaves the pointer nil": {
			input:   "!!null ''\n",
			wantNil: true,
		},
		"empty document leaves the pointer nil": {
			input:   "",
			wantNil: true,
		},
		"comment only leaves the pointer nil": {
			input:   "# c\n",
			wantNil: true,
		},
		"bare header leaves the pointer nil": {
			input:   "---\n",
			wantNil: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			var got *defaultsCafe

			require.NoError(t, doc.DecodeInto(t.Context(), &got, defaultsDecoding))

			if tc.wantNil {
				assert.Nil(t, got)

				return
			}

			want, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
			require.NoError(t, err)
			assert.Equal(t, &want, got)
		})
	}
}

func TestNode_Decode_Defaults(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  defaultsCafe
	}{
		"each value starts from the defaults of its place": {
			input: stringtest.LinesLF(
				"seats: 40",
				"receipts: &printer",
				"  host: printer.yamull",
				"kitchen: *printer",
				"stations:",
				"  - host: bar.yamull",
			),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Seats = 40
				c.Receipts = defaultsPrinter{Host: "printer.yamull", Port: 9100}
				c.Kitchen = defaultsPrinter{Host: "printer.yamull", Port: 9101}
				c.Stations = []defaultsPrinter{{Host: "bar.yamull", Port: 9100}}
			}),
		},
		"empty document": {
			input: "",
			want:  defaultedCafe(),
		},
		"null at the root": {
			input: "~\n",
			want:  defaultedCafe(),
		},
		"nulls": {
			input: stringtest.LinesLF(
				"seats: ~",
				"receipts: ~",
				"spare: ~",
				"stations: [~, {host: b}]",
				"backups: [~]",
				"printers: {bar: ~}",
			),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Spare = nil
				c.Stations = []defaultsPrinter{{Port: 9100}, {Host: "b", Port: 9100}}
				c.Backups = []*defaultsPrinter{nil}
				c.Printers = map[string]defaultsPrinter{"bar": {Port: 9100}}
			}),
		},
		"pointers": {
			input: stringtest.LinesLF(
				"owner: {host: o}",
				"spare: {port: 1}",
				"backups: [{host: b}]",
			),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Owner = &defaultsPrinter{Host: "o", Port: 9100}
				c.Spare = &defaultsPrinter{Host: "spare", Port: 1}
				c.Backups = []*defaultsPrinter{{Host: "b", Port: 9100}}
			}),
		},
		"map values": {
			input: stringtest.LinesLF(
				"printers:",
				"  bar: {host: b}",
				"  back: {host: k, port: 1}",
			),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Printers = map[string]defaultsPrinter{
					"bar":  {Host: "b", Port: 9100},
					"back": {Host: "k", Port: 1},
				}
			}),
		},
		"merge key": {
			input: stringtest.LinesLF(
				"stations:",
				"  - &bar {host: bar}",
				"  - {<<: *bar, port: 1}",
			),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Stations = []defaultsPrinter{{Host: "bar", Port: 9100}, {Host: "bar", Port: 1}}
			}),
		},
		"a slice replaces the default whole": {
			input: "stations: []\n",
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Stations = []defaultsPrinter{}
			}),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			got, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNode_DecodeInto_Defaults(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"receipts: {host: r}",
		"stations: [{host: s}]",
		"spare: {host: x}",
	))

	// The decode builds from the defaults only the values it creates.
	var got defaultsCafe

	require.NoError(t, doc.DecodeInto(t.Context(), &got, defaultsDecoding))
	assert.Equal(t, defaultsCafe{
		Receipts: defaultsPrinter{Host: "r"},
		Stations: []defaultsPrinter{{Host: "s", Port: 9100}},
		Spare:    &defaultsPrinter{Host: "x", Port: 9100},
	}, got)

	// A pointer the caller sets starts its target there, and the target
	// of the caller stays as it was.
	spare := &defaultsPrinter{Port: 7}
	held := defaultsCafe{Spare: spare}

	require.NoError(t, doc.DecodeInto(t.Context(), &held))
	assert.Equal(t, &defaultsPrinter{Host: "x", Port: 7}, held.Spare)
	assert.Equal(t, &defaultsPrinter{Port: 7}, spare)

	// A caller that starts from Defaults gets what Decode returns.
	start := niceyaml.Defaults[defaultsCafe](defaultsRules...)
	require.NoError(t, doc.DecodeInto(t.Context(), &start, defaultsDecoding))

	want, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
	require.NoError(t, err)
	assert.Equal(t, want, start)
}

func TestNode_DecodeInto_StartsWithoutDefaults(t *testing.T) {
	t.Parallel()

	type named struct {
		Name string `yaml:"name"`
		X    int    `yaml:"x"`
	}

	type box struct {
		Inl  named  `yaml:",inline"`
		Name string `yaml:"name"`
	}

	type plain struct {
		Name  string `yaml:"name"`
		Codes []int  `yaml:"codes"`
	}

	type pair struct {
		A plain `yaml:"a"`
		B plain `yaml:"b"`
	}

	type tagged struct {
		Tags defaultsBag `yaml:"tags"`
		Name string      `yaml:"name"`
	}

	type tagBox struct {
		Inl  tagged      `yaml:",inline"`
		Tags defaultsBag `yaml:"tags"`
	}

	// A target that the caller fills follows the rules of start values
	// with no TypeDefaults rule at all.
	tcs := map[string]struct {
		start any
		want  any
		input string
	}{
		"field of a struct held inline that the box names too": {
			input: "name: a\nx: 1\n",
			start: &box{Inl: named{Name: "pre"}},
			want:  &box{Inl: named{Name: "pre", X: 1}, Name: "a"},
		},
		// The method of the tags of the inline struct writes into the map
		// that the target holds, and the field keeps the start all the
		// same.
		"field that decodes itself in a struct held inline that the box names too": {
			input: "name: a\ntags: {k: v}\n",
			start: &tagBox{Inl: tagged{Tags: defaultsBag{"pre": 1}}},
			want: &tagBox{
				Inl:  tagged{Tags: defaultsBag{"pre": 1}, Name: "a"},
				Tags: defaultsBag{"k": "v", "calls": 1},
			},
		},
		"alias whose place starts from another value": {
			input: "a: &a {codes: [1]}\nb: *a\n",
			start: &pair{B: plain{Name: "keep"}},
			want: &pair{
				A: plain{Codes: []int{1}},
				B: plain{Name: "keep", Codes: []int{1}},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			require.NoError(t, doc.DecodeInto(t.Context(), tc.start))
			assert.Equal(t, tc.want, tc.start)
		})
	}
}

func TestNode_Decode_DefaultsAtAliases(t *testing.T) {
	t.Parallel()

	type pair struct {
		A *defaultsPrinter `yaml:"a"`
		B *defaultsPrinter `yaml:"b"`
		C *defaultsPrinter `yaml:"c"`
		X any              `yaml:"x"`
		Y any              `yaml:"y"`
	}

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"a: &p {host: h}",
		"b: *p",
		"c: *p",
		"x: &m {k: v}",
		"y: *m",
	))

	set := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(p *pair) { p.C = &defaultsPrinter{Port: 1} }))

	got, err := doc.Decode[pair](t.Context(), defaultsDecoding, set)
	require.NoError(t, err)

	// The decoder gives a and b one target, because the two places start
	// from equal values. The place c starts from another value, so the
	// decoder decodes the anchor again for it.
	assert.Equal(t, &defaultsPrinter{Host: "h", Port: 9100}, got.A)
	assert.Same(t, got.A, got.B)
	assert.Equal(t, &defaultsPrinter{Host: "h", Port: 1}, got.C)

	// An interface value holds the value of its anchor.
	assert.Equal(t, map[string]any{"k": "v"}, got.Y)

	t.Run("value whose place starts apart from its anchor", func(t *testing.T) {
		t.Parallel()

		type plain struct {
			Labels map[string]string `yaml:"labels"`
			Codes  []int             `yaml:"codes"`
			Name   string            `yaml:"name"`
		}

		type holder struct {
			Q plain `yaml:"q"`
		}

		// The field s comes before the field r, as in the example of the
		// Defaults section of the package documentation.
		type root struct {
			S holder `yaml:"s"`
			R holder `yaml:"r"`
		}

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"s: &s {q: {codes: [1]}}",
			"r: *s",
		))

		got, err := doc.Decode[root](t.Context(), niceyaml.OverrideTypeRules(
			niceyaml.TypeDefaults(func(r *root) {
				r.S.Q.Name = "q"
				r.S.Q.Labels = map[string]string{"a": "b"}
			}),
		))
		require.NoError(t, err)

		// The place r starts from zero, and the place of the anchor does
		// not, so the decoder decodes the anchor again for r. The value at r
		// does not hold the name or the labels of s.
		assert.Equal(t, plain{Codes: []int{1}}, got.R.Q)
		assert.Equal(t, plain{Labels: map[string]string{"a": "b"}, Codes: []int{1}, Name: "q"}, got.S.Q)
	})

	// The function of defaultsBag sets a new map on each call.
	bagDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *defaultsBag) {
		*b = defaultsBag{"d": 1}
	}))

	// Each place below starts from the defaults of the bag, and its
	// method runs once on that start.
	bag := defaultsBag{"d": 1, "name": "n", "calls": 1}

	t.Run("value that writes into its start", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &x {name: n}",
			"b: *x",
		))

		got, err := doc.Decode[struct {
			A defaultsBag `yaml:"a"`
			B defaultsBag `yaml:"b"`
		}](t.Context(), bagDefaults)
		require.NoError(t, err)

		// The method of a writes into the map that a starts from. The
		// place b starts from a value equal to the start of a, so it
		// shares the value of a, and the method runs once.
		assert.Equal(t, bag, got.A)
		assert.Equal(t, reflect.ValueOf(got.A).Pointer(), reflect.ValueOf(got.B).Pointer())
	})

	t.Run("value that writes into the slice of its start", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &x n",
			"b: *x",
		))

		got, err := doc.Decode[struct {
			A defaultsTally `yaml:"a"`
			B defaultsTally `yaml:"b"`
		}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(c *defaultsTally) {
			c.Calls = []int{0}
		})))
		require.NoError(t, err)

		// The method of a writes into the slice that a starts from. The
		// place b starts from a value equal to the start of a, so it
		// shares the value of a, and the method runs once.
		assert.Equal(t, defaultsTally{Calls: []int{1}, Name: "n"}, got.A)
		assert.Same(t, &got.A.Calls[0], &got.B.Calls[0])
	})

	t.Run("defaults that one function sets", func(t *testing.T) {
		t.Parallel()

		type hooked struct {
			Hook  func()  `yaml:"-"`
			Codes []int   `yaml:"codes"`
			Ratio float64 `yaml:"ratio"`
		}

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &a {codes: [1]}",
			"b: *a",
		))

		tcs := map[string]struct {
			fn func(*hooked)
		}{
			"func": {fn: func(h *hooked) { h.Hook = func() {} }},
			"NaN":  {fn: func(h *hooked) { h.Ratio = math.NaN() }},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := doc.Decode[struct {
					A hooked `yaml:"a"`
					B hooked `yaml:"b"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(tc.fn)))
				require.NoError(t, err)

				// The places a and b start from the defaults that one
				// function sets, so b shares the value of a.
				assert.Equal(t, []int{1}, got.A.Codes)
				assert.Same(t, &got.A.Codes[0], &got.B.Codes[0])
			})
		}
	})

	t.Run("places of another type than the place of the anchor", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"x: &a {name: n}",
			"y: *a",
			"l: [*a]",
		))

		got, err := doc.Decode[struct {
			X map[string]any `yaml:"x"`
			Y defaultsBag    `yaml:"y"`
			L []defaultsBag  `yaml:"l"`
		}](t.Context(), bagDefaults)
		require.NoError(t, err)

		// The place x starts from zero, and the places y and l[0] start
		// from the defaults of the bag, so each decodes the anchor again.
		assert.Equal(t, map[string]any{"name": "n"}, got.X)
		assert.Equal(t, bag, got.Y)
		assert.Equal(t, []defaultsBag{bag}, got.L)
	})

	t.Run("anchor in an any value", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"x: {in: &a {name: n}}",
			"y: *a",
			"z: *a",
		))

		got, err := doc.Decode[struct {
			X any         `yaml:"x"`
			Y defaultsBag `yaml:"y"`
			Z defaultsBag `yaml:"z"`
		}](t.Context(), bagDefaults)
		require.NoError(t, err)

		// The decoder reads the anchor into a value inside the any value
		// at x, and that value started from nil. Each alias thus decodes
		// the anchor again from the defaults of the bag.
		assert.Equal(t, map[string]any{"in": map[string]any{"name": "n"}}, got.X)
		assert.Equal(t, bag, got.Y)
		assert.Equal(t, bag, got.Z)
		assert.NotEqual(t, reflect.ValueOf(got.Y).Pointer(), reflect.ValueOf(got.Z).Pointer())
	})

	t.Run("anchor read again into an any value", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &outer {in: {deep: &inner {name: n}}}",
			"y: *outer",
			"c: *inner",
		))

		got, err := doc.Decode[struct {
			A struct {
				In map[string]defaultsBag `yaml:"in"`
			} `yaml:"a"`
			Y struct {
				In any `yaml:"in"`
			} `yaml:"y"`
			C defaultsBag `yaml:"c"`
		}](t.Context(), bagDefaults)
		require.NoError(t, err)

		// The decoder first reads inner into a bag at a.in.deep. The
		// place y has another type than a, so y decodes outer again, and
		// reads inner into a value inside the any value at y.in. That
		// value started from nil, so c decodes inner again from the
		// defaults of the bag.
		assert.Equal(t, map[string]defaultsBag{"deep": bag}, got.A.In)
		assert.Equal(t, map[string]any{"deep": map[string]any{"name": "n"}}, got.Y.In)
		assert.Equal(t, bag, got.C)
	})
}

func TestNode_Decode_DefaultsShareValuesOfAnchors(t *testing.T) {
	t.Parallel()

	type retry struct {
		Codes []int `yaml:"codes"`
		Max   int   `yaml:"max"`
	}

	type service struct {
		Env   map[string]string `yaml:"env"`
		Retry *retry            `yaml:"retry"`
		Hooks []retry           `yaml:"hooks"`
		Image string            `yaml:"image"`
	}

	type compose struct {
		Services map[string]service `yaml:"services"`
	}

	// The fields of shared name the values of the api service that the
	// worker service shares.
	type shared struct {
		Retry, Hooks, Env bool
	}

	const common = "{env: {LOG: info}, retry: {codes: [500]}, hooks: [{codes: [1]}]}"

	aliasOfPlace := stringtest.LinesLF(
		"services: {api: &common " + common + ", worker: *common}",
	)
	mergeKeys := stringtest.LinesLF(
		"x-common: &common "+common,
		"services: {api: {<<: *common, image: api}, worker: {<<: *common, image: worker}}",
	)
	aliases := stringtest.LinesLF(
		"x-common: &common "+common,
		"services: {api: *common, worker: *common}",
	)

	rules := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(r *retry) { r.Max = 3 }))

	tcs := map[string]struct {
		input   string
		opts    []niceyaml.DecodeOption
		wantMax int
		want    shared
	}{
		"alias to an anchor of the same type": {
			input: aliasOfPlace,
			want:  shared{Retry: true, Hooks: true, Env: true},
		},
		"alias to an anchor of the same type with defaults": {
			input:   aliasOfPlace,
			opts:    []niceyaml.DecodeOption{rules},
			wantMax: 3,
			want:    shared{Retry: true, Hooks: true, Env: true},
		},
		"merge keys": {
			input: mergeKeys,
		},
		// The decoder gives each service values of its own.
		"merge keys with defaults": {
			input:   mergeKeys,
			opts:    []niceyaml.DecodeOption{rules},
			wantMax: 3,
		},
		"aliases to an anchor outside the services": {
			input: aliases,
		},
		"aliases to an anchor outside the services with defaults": {
			input:   aliases,
			opts:    []niceyaml.DecodeOption{rules},
			wantMax: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, tc.input).Decode[compose](t.Context(), tc.opts...)
			require.NoError(t, err)

			api, worker := got.Services["api"], got.Services["worker"]
			for _, s := range []service{api, worker} {
				require.Equal(t, map[string]string{"LOG": "info"}, s.Env)
				require.Equal(t, &retry{Codes: []int{500}, Max: tc.wantMax}, s.Retry)
				require.Equal(t, []retry{{Codes: []int{1}, Max: tc.wantMax}}, s.Hooks)
			}

			assert.Equal(t, tc.want, shared{
				Retry: api.Retry == worker.Retry,
				Hooks: &api.Hooks[0] == &worker.Hooks[0],
				Env:   reflect.ValueOf(api.Env).Pointer() == reflect.ValueOf(worker.Env).Pointer(),
			})
		})
	}
}

func TestNode_Decode_DefaultsShareStartsOfAnchors(t *testing.T) {
	t.Parallel()

	type leaf struct {
		Max int `yaml:"max"`
	}

	type retry struct {
		Codes []int `yaml:"codes"`
		Max   int   `yaml:"max"`
	}

	// The type bare holds no memory that the decoder gives it.
	type bare struct {
		Env   map[string]string `yaml:"env"`
		Image string            `yaml:"image"`
		Port  int               `yaml:"port"`
	}

	// The function sets a new map on each call.
	bareDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *bare) {
		b.Env = map[string]string{}
	}))

	// The function apart checks that a and b are two maps.
	apart := func(t *testing.T, a, b map[string]string) {
		t.Helper()

		require.NotNil(t, a)
		require.NotNil(t, b)
		assert.NotEqual(t, reflect.ValueOf(a).Pointer(), reflect.ValueOf(b).Pointer())
	}

	// The function same checks that a and b are one map.
	same := func(t *testing.T, a, b map[string]string) {
		t.Helper()

		require.NotNil(t, a)
		assert.Equal(t, reflect.ValueOf(a).Pointer(), reflect.ValueOf(b).Pointer())
	}

	// Each place starts from memory that its defaults set. An alias whose
	// place starts from a value equal to the start of the place of its
	// anchor takes the value of the anchor, with that memory. An alias that
	// reads an anchor that no place of the type decodes gets a value of its
	// own.
	tcs := map[string]struct {
		test  func(t *testing.T, doc *niceyaml.Node)
		input string
	}{
		"alias to an anchor on a decoded place": {
			input: "services:\n  api: &c {retry: {codes: [500]}, image: x}\n  worker: *c\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type service struct {
					Env   map[string]string `yaml:"env"`
					Retry *retry            `yaml:"retry"`
					Image string            `yaml:"image"`
				}

				type compose struct {
					Services map[string]service `yaml:"services"`
				}

				plain, err := doc.Decode[compose](t.Context())
				require.NoError(t, err)
				require.Same(t, plain.Services["api"].Retry, plain.Services["worker"].Retry)

				got, err := doc.Decode[compose](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(s *service) { s.Env = map[string]string{} }),
					niceyaml.TypeDefaults(func(r *retry) { r.Max = 3 }),
				))
				require.NoError(t, err)

				// The decoder gives the services one retry target and one
				// map of the defaults.
				api, worker := got.Services["api"], got.Services["worker"]
				assert.Same(t, api.Retry, worker.Retry)
				assert.Equal(t, &retry{Codes: []int{500}, Max: 3}, api.Retry)
				same(t, api.Env, worker.Env)
			},
		},
		"aliases to an anchor whose alias the decoder reads first": {
			input: "t: &t {codes: [1]}\nx-item: &item {meta: *t, leaf: {}}\nfirst: *item\nsecond: *item\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type plain struct {
					Codes []int `yaml:"codes"`
				}

				type item struct {
					Meta any   `yaml:"meta"`
					Leaf *leaf `yaml:"leaf"`
				}

				type root struct {
					First  item  `yaml:"first"`
					T      plain `yaml:"t"`
					Second item  `yaml:"second"`
				}

				without, err := doc.Decode[root](t.Context())
				require.NoError(t, err)

				// The decoder reads first before the place of t, so the
				// alias in first decodes as a map.
				require.IsType(t, map[string]any{}, without.First.Meta)
				require.IsType(t, plain{}, without.Second.Meta)

				got, err := doc.Decode[root](t.Context(),
					niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(l *leaf) { l.Max = 3 })))
				require.NoError(t, err)

				assert.IsType(t, without.First.Meta, got.First.Meta)
				assert.IsType(t, without.Second.Meta, got.Second.Meta)
				assert.Equal(t, &leaf{Max: 3}, got.First.Leaf)
				assert.Equal(t, &leaf{Max: 3}, got.Second.Leaf)
				assert.NotSame(t, got.First.Leaf, got.Second.Leaf)
			},
		},
		"struct without memory at aliases to an anchor that the type does not read": {
			input: "x-c: &c {image: x, port: 1}\nservices: {a: *c, b: *c}\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[struct {
					Services map[string]bare `yaml:"services"`
				}](t.Context(), bareDefaults)
				require.NoError(t, err)

				assert.Equal(t, bare{Env: map[string]string{}, Image: "x", Port: 1}, got.Services["a"])
				assert.Equal(t, got.Services["a"], got.Services["b"])
				apart(t, got.Services["a"].Env, got.Services["b"].Env)
			},
		},
		"struct without memory at an alias to an anchor on a decoded place": {
			input: "services: {a: &c {image: x, port: 1}, b: *c}\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[struct {
					Services map[string]bare `yaml:"services"`
				}](t.Context(), bareDefaults)
				require.NoError(t, err)

				assert.Equal(t, bare{Env: map[string]string{}, Image: "x", Port: 1}, got.Services["a"])
				assert.Equal(t, got.Services["a"], got.Services["b"])
				same(t, got.Services["a"].Env, got.Services["b"].Env)
			},
		},
		"struct fields": {
			input: "api: &c {image: x}\nworker: *c\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type pair struct {
					API    bare `yaml:"api"`
					Worker bare `yaml:"worker"`
				}

				got, err := doc.Decode[pair](t.Context(), bareDefaults)
				require.NoError(t, err)

				// Each field starts from equal defaults.
				assert.Equal(t, "x", got.Worker.Image)
				same(t, got.API.Env, got.Worker.Env)
			},
		},
		"pointer that separate decodes share": {
			input: "x-c: &c {addr: 10.0.0.1}\nservices: {a: *c, b: *c}\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type host struct {
					Env  map[string]string `yaml:"env"`
					Addr netip.Addr        `yaml:"addr"`
				}

				got, err := doc.Decode[struct {
					Services map[string]host `yaml:"services"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(h *host) {
					h.Env = map[string]string{}
				})))
				require.NoError(t, err)

				assert.Equal(t, netip.MustParseAddr("10.0.0.1"), got.Services["b"].Addr)
				apart(t, got.Services["a"].Env, got.Services["b"].Env)
			},
		},
		"start inside an array": {
			input: "vals: [&a {arr: [{max: 1}, {}], name: x}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type tagged struct {
					Labels map[string]string `yaml:"labels"`
					Max    int               `yaml:"max"`
				}

				type flat struct {
					Arr  [2]tagged `yaml:"arr"`
					Name string    `yaml:"name"`
				}

				calls := 0

				got, err := doc.Decode[struct {
					Vals []flat `yaml:"vals"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(g *tagged) {
					calls++
					g.Labels = map[string]string{}
				})))
				require.NoError(t, err)
				require.Len(t, got.Vals, 3)

				// The function runs for the two elements of the anchor
				// alone.
				assert.Equal(t, 2, calls)
				assert.Equal(t, 1, got.Vals[1].Arr[0].Max)
				same(t, got.Vals[0].Arr[1].Labels, got.Vals[1].Arr[1].Labels)
			},
		},
		// The function of the struct sets the map, so the array holds what
		// the start of the struct holds there.
		"start inside an array that the document does not write": {
			input: "vals: [&a {name: x}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type tagged struct {
					Labels map[string]string `yaml:"labels"`
					Max    int               `yaml:"max"`
				}

				type flat struct {
					Arr  [2]tagged `yaml:"arr"`
					Name string    `yaml:"name"`
				}

				calls := 0

				got, err := doc.Decode[struct {
					Vals []flat `yaml:"vals"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(f *flat) {
					calls++
					f.Arr[1].Labels = map[string]string{}
				})))
				require.NoError(t, err)
				require.Len(t, got.Vals, 3)

				// The function runs for the anchor alone.
				assert.Equal(t, 1, calls)
				assert.Equal(t, "x", got.Vals[1].Name)
				same(t, got.Vals[0].Arr[1].Labels, got.Vals[1].Arr[1].Labels)
			},
		},
		"pointer in a struct in an interface value in an array": {
			input: "vals: [&a {kids: [{}]}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type holder struct {
					P *int
				}

				type box struct {
					Box  [1]any `yaml:"box"`
					Kids []leaf `yaml:"kids"`
				}

				got, err := doc.Decode[struct {
					Vals []box `yaml:"vals"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *box) {
					b.Box[0] = holder{P: new(int)}
				})))
				require.NoError(t, err)
				require.Len(t, got.Vals, 3)

				first, ok := got.Vals[0].Box[0].(holder)
				require.True(t, ok)

				second, ok := got.Vals[1].Box[0].(holder)
				require.True(t, ok)

				assert.Same(t, first.P, second.P)
				assert.Equal(t, []leaf{{}}, got.Vals[1].Kids)
			},
		},
		"start in an unexported field": {
			input: "vals: [&a {name: x}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type hidden struct {
					hidden map[string]string
					Name   string `yaml:"name"`
					Max    int    `yaml:"max"`
				}

				got, err := doc.Decode[struct {
					Vals []hidden `yaml:"vals"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(h *hidden) {
					h.hidden = map[string]string{}
					h.Max = 3
				})))
				require.NoError(t, err)
				require.Len(t, got.Vals, 3)

				assert.Equal(t, "x", got.Vals[1].Name)
				assert.Equal(t, 3, got.Vals[1].Max)
				same(t, got.Vals[0].hidden, got.Vals[1].hidden)
			},
		},
		// Each row below sets one kind of memory in a struct inside an
		// array.
		"func in an array": {
			input: "vals: [&a {arr: [{max: 1}], name: x}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type hook struct {
					Fn  func() int
					Max int `yaml:"max"`
				}

				// Each call sets a func with a counter of its own.
				got := defaultsInArrays[hook](t, doc, func(h *hook) {
					calls := 0

					h.Fn = func() int {
						calls++

						return calls
					}
				})

				require.NotNil(t, got[0].Fn)
				require.NotNil(t, got[1].Fn)
				// The aliases share the func of the anchor, and so its
				// counter.
				assert.Equal(t, 1, got[0].Fn())
				assert.Equal(t, 2, got[1].Fn())
				assert.Equal(t, 1, got[1].Max)
			},
		},
		"channel in an array": {
			input: "vals: [&a {arr: [{max: 1}], name: x}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type hook struct {
					Ch  chan int
					Max int `yaml:"max"`
				}

				got := defaultsInArrays[hook](t, doc, func(h *hook) { h.Ch = make(chan int) })

				require.NotNil(t, got[0].Ch)
				require.NotNil(t, got[1].Ch)
				assert.Equal(t, got[0].Ch, got[1].Ch)
				assert.Equal(t, 1, got[1].Max)
			},
		},
		"unsafe pointer in an array": {
			input: "vals: [&a {arr: [{max: 1}], name: x}, *a, *a]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type hook struct {
					U   unsafe.Pointer
					Max int `yaml:"max"`
				}

				got := defaultsInArrays[hook](t, doc, func(h *hook) { h.U = unsafe.Pointer(new(int)) })

				require.NotNil(t, got[0].U)
				require.NotNil(t, got[1].U)
				assert.Equal(t, got[0].U, got[1].U)
				assert.Equal(t, 1, got[1].Max)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, yamltest.FirstDocument(t, tc.input))
		})
	}
}

func TestNode_Decode_DefaultsAtReferenceAliases(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"stations: [*bar, {host: b}]",
		"printers: {main: *bar}",
		"owner: *bar",
		"backups: [*bar, *bar]",
		"kitchen: *bar",
		"labels: *labels",
	), yamltest.References(stringtest.LinesLF(
		"bar: &bar {host: bar.yamull}",
		"labels: &labels {floor: '1'}",
	)))

	got, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
	require.NoError(t, err)

	// Each value at an alias starts from the defaults of its place, as at
	// an alias of the document.
	bar := defaultsPrinter{Host: "bar.yamull", Port: 9100}
	assert.Equal(t, defaultedCafe(func(c *defaultsCafe) {
		c.Stations = []defaultsPrinter{bar, {Host: "b", Port: 9100}}
		c.Printers = map[string]defaultsPrinter{"main": bar}
		c.Owner = &bar
		c.Backups = []*defaultsPrinter{&bar, &bar}
		c.Kitchen = defaultsPrinter{Host: "bar.yamull", Port: 9101}
		c.Labels = map[string]string{"floor": "1"}
	}), got)

	// The decoder gives the two places two targets, so each place builds a
	// target of its own, with equal content.
	assert.NotSame(t, got.Backups[0], got.Backups[1])

	// A decode of the Node of the alias starts from the same defaults.
	at := yamltest.At(t, doc, paths.MustParse("$.stations[0]"))

	station, err := at.Decode[defaultsPrinter](t.Context(), defaultsDecoding)
	require.NoError(t, err)
	assert.Equal(t, got.Stations[0], station)

	t.Run("alias to a null", func(t *testing.T) {
		t.Parallel()

		// A null of a reference document leaves each element and map value
		// at the defaults of its type, as a null of the document does, and
		// sets a pointer to nil.
		nulls := yamltest.References("nul: &nul null\ntil: &til ~\n")

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"stations: [*nul, *til]",
			"printers: {main: *nul}",
			"owner: *nul",
		), nulls)

		got, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
		require.NoError(t, err)
		assert.Equal(t, defaultedCafe(func(c *defaultsCafe) {
			c.Stations = []defaultsPrinter{{Port: 9100}, {Port: 9100}}
			c.Printers = map[string]defaultsPrinter{"main": {Port: 9100}}
		}), got)

		ints, err := yamltest.FirstDocument(t, "ints: [*nul]\n", nulls).Decode[struct {
			Ints []int `yaml:"ints"`
		}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(i *int) { *i = 7 })))
		require.NoError(t, err)
		assert.Equal(t, []int{7}, ints.Ints)
	})
}

func TestNode_Decode_DefaultsInsideReferenceAnchors(t *testing.T) {
	t.Parallel()

	// Each value that the decoder creates inside the anchor starts from
	// the defaults of its type, in a decode of the document and in a decode
	// of the Node of the alias.
	doc := yamltest.FirstDocument(t, "stations: *crew\n",
		yamltest.References("crew: &crew [{host: a}]\n"))

	got, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
	require.NoError(t, err)
	assert.Equal(t, []defaultsPrinter{{Host: "a", Port: 9100}}, got.Stations)

	at := yamltest.At(t, doc, paths.MustParse("$.stations"))

	stations, err := at.Decode[[]defaultsPrinter](t.Context(), defaultsDecoding)
	require.NoError(t, err)
	assert.Equal(t, got.Stations, stations)
}

func TestNode_Decode_DefaultsReferenceMerges(t *testing.T) {
	t.Parallel()

	references := yamltest.References(stringtest.LinesLF(
		"port: &port {port: 1}",
		"host: &host {host: h}",
		"seats: &seats {seats: 2}",
		"crew: &crew {stations: [{host: c}]}",
	))

	tcs := map[string]struct {
		input string
		want  defaultsCafe
	}{
		// The kitchen field starts from its defaults, and the merge key
		// writes its port.
		"field after a merge key": {
			input: "kitchen: {<<: *port, host: k}\n",
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Kitchen = defaultsPrinter{Host: "k", Port: 1}
			}),
		},
		"field before a merge key": {
			input: "kitchen: {host: k, <<: *port}\n",
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Kitchen = defaultsPrinter{Host: "k", Port: 1}
			}),
		},
		// The decoder starts each element from its defaults.
		"element that merges a reference document": {
			input: "stations: [{<<: *port, host: s}, {<<: *host}]\n",
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Stations = []defaultsPrinter{{Host: "s", Port: 1}, {Host: "h", Port: 9100}}
			}),
		},
		"target of a pointer that merges a reference document": {
			input: "owner: {<<: *host}\n",
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Owner = &defaultsPrinter{Host: "h", Port: 9100}
			}),
		},
		// The reference document does not hold the keys of the entries
		// before the merge key, so the decoder reads those entries, and the
		// values it creates there start from their defaults.
		"entries before a merge key that does not hold their keys": {
			input: stringtest.LinesLF(
				"stations: [{host: s}]",
				"printers: {main: {host: p}}",
				"owner: {host: o}",
				"<<: *seats",
			),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Stations = []defaultsPrinter{{Host: "s", Port: 9100}}
				c.Printers = map[string]defaultsPrinter{"main": {Host: "p", Port: 9100}}
				c.Owner = &defaultsPrinter{Host: "o", Port: 9100}
				c.Seats = 2
			}),
		},
		// The decoder reads the stations of the reference document, and
		// each element there starts from its defaults.
		"entry before a merge key that replaces it": {
			input: stringtest.LinesLF("stations: [{host: s}]", "<<: *crew"),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Stations = []defaultsPrinter{{Host: "c", Port: 9100}}
			}),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, references)

			got, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNode_Decode_DefaultsOfFieldsBesideReferenceMerges(t *testing.T) {
	t.Parallel()

	type hook struct {
		Codes []int `yaml:"codes"`
		Max   int   `yaml:"max"`
	}

	type shelf struct {
		Meta   map[string]any  `yaml:"meta"`
		Owner  *hook           `yaml:"owner"`
		Host   string          `yaml:"host"`
		Copies []int           `yaml:"copies"`
		Self   defaultsCounted `yaml:"self"`
		Fixed  hook            `yaml:"fixed"`
	}

	type held struct {
		Note  string `yaml:"note"`
		Shelf shelf  `yaml:",inline"`
	}

	type aliased struct {
		Base  map[string]any `yaml:",inline,alias"`
		Host  string         `yaml:"host"`
		Owner *hook          `yaml:"owner"`
	}

	// The decoder reads the fields in this order. It thus reads the lists
	// before the places of the anchors that the lists read.
	type shelves struct {
		S     []shelf        `yaml:"s"`
		H     []held         `yaml:"h"`
		A     []aliased      `yaml:"a"`
		M     map[string]any `yaml:"m"`
		Spare *hook          `yaml:"spare"`
		Nums  []int          `yaml:"nums"`
	}

	type plain struct {
		Codes []int  `yaml:"codes"`
		Name  string `yaml:"name"`
	}

	type item struct {
		Q     plain  `yaml:"q"`
		Host  string `yaml:"host"`
		Owner *hook  `yaml:"owner"`
	}

	type pair struct {
		A item `yaml:"a"`
		B item `yaml:"b"`
	}

	hookDefaults := niceyaml.TypeDefaults(func(h *hook) { h.Max = 3 })

	// The anchors that the element of the list reads.
	const anchors = "spare: &o {codes: [1]}\nnums: &n [7]\n"

	// Each struct here merges the reference document. Each named field
	// that the document writes there takes the value that the same document
	// without the merge key gives it. Each field that the merge key brings
	// in starts from the value that its struct holds there, in the struct or
	// in an inline struct.
	tcs := map[string]struct {
		test  func(t *testing.T, doc *niceyaml.Node)
		input string
	}{
		"aliases to anchors that the decoder reads later": {
			input: anchors + "s: [{<<: *bar, meta: {k: *o}, copies: *n}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				for _, opts := range [][]niceyaml.DecodeOption{nil, {niceyaml.OverrideTypeRules(hookDefaults)}} {
					got, err := doc.Decode[shelves](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.S, 1)
					require.Len(t, got.S[0].Copies, 1)
					require.Len(t, got.Nums, 1)

					// The decoder reads each alias in the list before the
					// place of its anchor, so it gives the alias a value of
					// its own.
					assert.IsType(t, map[string]any{}, got.S[0].Meta["k"])
					assert.NotSame(t, &got.Nums[0], &got.S[0].Copies[0])
					assert.Equal(t, "h", got.S[0].Host)
					assert.Nil(t, got.S[0].Owner)
				}
			},
		},
		"pointer that the document writes": {
			input: anchors + "s: [{<<: *bar, meta: {k: *o}, copies: *n, owner: {codes: [2]}}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[shelves](t.Context(), niceyaml.OverrideTypeRules(hookDefaults))
				require.NoError(t, err)
				require.Len(t, got.S, 1)

				// The target starts from the defaults of its type.
				assert.Equal(t, &hook{Codes: []int{2}, Max: 3}, got.S[0].Owner)
			},
		},
		"struct at an alias": {
			input: "a: &x {<<: *bar, q: {codes: [1]}}\nb: *x\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[pair](t.Context(), niceyaml.OverrideTypeRules(
					hookDefaults,
					niceyaml.TypeDefaults(func(p *pair) { p.A.Q.Name = "q" }),
				))
				require.NoError(t, err)
				require.Len(t, got.A.Q.Codes, 1)
				require.Len(t, got.B.Q.Codes, 1)

				// The place of the alias starts from another value than the
				// place of the anchor, so the decoder decodes the anchor
				// again from that value.
				assert.Equal(t, "q", got.A.Q.Name)
				assert.Empty(t, got.B.Q.Name)
				assert.Equal(t, "h", got.B.Host)
				assert.NotSame(t, &got.A.Q.Codes[0], &got.B.Q.Codes[0])
			},
		},
		"value that decodes itself": {
			input: anchors + "s: [{<<: *bar, meta: {k: *o}, copies: *n, self: x}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				calls := 0
				got, err := doc.Decode[shelves](t.Context(), niceyaml.OverrideTypeRules(
					hookDefaults,
					niceyaml.TypeDefaults(func(c *defaultsCounted) {
						c.Calls = &calls
						c.Port = 5
					}),
				))
				require.NoError(t, err)
				require.Len(t, got.S, 1)

				// The decoder calls the method once, on a field that starts
				// from its defaults.
				assert.Equal(t, 1, calls)
				assert.Equal(t, "x", got.S[0].Self.Name)
				assert.Equal(t, 5, got.S[0].Self.Port)
			},
		},
		"field that the merge key brings in": {
			input: "s: [{<<: *fix, host: x}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[shelves](t.Context(), niceyaml.OverrideTypeRules(hookDefaults))
				require.NoError(t, err)
				require.Len(t, got.S, 1)

				// The field starts from the defaults of its type.
				assert.Equal(t, hook{Codes: []int{4}, Max: 3}, got.S[0].Fixed)
				assert.Equal(t, "x", got.S[0].Host)
			},
		},
		"field of an inline struct": {
			input: "h: [{<<: *fix, note: n}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[shelves](t.Context(), niceyaml.OverrideTypeRules(hookDefaults))
				require.NoError(t, err)
				require.Len(t, got.H, 1)

				// The field starts from the defaults of its type.
				assert.Equal(t, hook{Codes: []int{4}, Max: 3}, got.H[0].Shelf.Fixed)
				assert.Equal(t, "n", got.H[0].Note)
			},
		},
		"field that reads the anchor of the merge key": {
			input: "m: &own {<<: *bar}\na: [{<<: *own}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				got, err := doc.Decode[shelves](t.Context(), niceyaml.OverrideTypeRules(hookDefaults))
				require.NoError(t, err)
				require.Len(t, got.A, 1)

				// The decoder reads the field before the place of the
				// anchor, so the field holds a map of its own.
				assert.Equal(t, got.M, got.A[0].Base)
				assert.NotEqual(t, reflect.ValueOf(got.M).Pointer(), reflect.ValueOf(got.A[0].Base).Pointer())
				assert.Equal(t, "h", got.A[0].Host)
			},
		},
		// The decode reads the aliases after the struct that merges the
		// reference document, outside that struct.
		"aliases after a field held inline beside a reference merge": {
			input: "ws: [{<<: *bar, name: x}]\nx-c: &c {image: i}\nsvcs: [*c, *c]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type with struct {
					Extra *map[string]string `yaml:",inline"`
					Host  string             `yaml:"host"`
					Name  string             `yaml:"name"`
				}

				type svc struct {
					Env   map[string]string `yaml:"env"`
					Image string            `yaml:"image"`
				}

				got, err := doc.Decode[struct {
					Ws   []with `yaml:"ws"`
					Svcs []svc  `yaml:"svcs"`
				}](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(s *svc) { s.Env = map[string]string{} }),
					niceyaml.TypeDefaults(func(w *with) { w.Name = "d" }),
				))
				require.NoError(t, err)
				require.Len(t, got.Ws, 1)
				require.Len(t, got.Svcs, 2)

				assert.Equal(t, "h", got.Ws[0].Host)
				assert.Equal(t, "x", got.Ws[0].Name)
				assert.Equal(t, "i", got.Svcs[1].Image)

				// Each service keeps the map that its own defaults set.
				require.NotNil(t, got.Svcs[0].Env)
				require.NotNil(t, got.Svcs[1].Env)
				assert.NotEqual(t,
					reflect.ValueOf(got.Svcs[0].Env).Pointer(),
					reflect.ValueOf(got.Svcs[1].Env).Pointer())
			},
		},
		// Each alias starts from the defaults of its type, as the anchor
		// does, so it takes the value of the anchor. The function thus runs
		// for the two elements of the anchor alone, as it does without the
		// merge key.
		"start inside an array inside a struct held inline beside a reference merge": {
			input: "ws: [{<<: *bar, name: w, items: [&a {arr: [{max: 1}, {}], name: x}, *a, *a]}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type tagged struct {
					Labels map[string]string `yaml:"labels"`
					Max    int               `yaml:"max"`
				}

				type flat struct {
					Arr  [2]tagged `yaml:"arr"`
					Name string    `yaml:"name"`
				}

				type with struct {
					Extra struct {
						Items []flat `yaml:"items"`
					} `yaml:",inline"`
					Host string `yaml:"host"`
					Name string `yaml:"name"`
				}

				calls := 0

				got, err := doc.Decode[struct {
					Ws []with `yaml:"ws"`
				}](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(g *tagged) {
						calls++
						g.Labels = map[string]string{}
					}),
					niceyaml.TypeDefaults(func(w *with) { w.Name = "d" }),
				))
				require.NoError(t, err)
				require.Len(t, got.Ws, 1)
				require.Len(t, got.Ws[0].Extra.Items, 3)

				assert.Equal(t, "h", got.Ws[0].Host)
				assert.Equal(t, "x", got.Ws[0].Extra.Items[1].Name)
				assert.Equal(t, 2, calls)
			},
		},
		// The aliases take the value of the anchor, so the function runs for
		// the two elements of the anchor alone.
		"start inside an array inside a map held inline beside a reference merge": {
			input: "ws: [{<<: *lst, items: [&a {arr: [{max: 1}, {}], name: x}, *a, *a]}]\n",
			test: func(t *testing.T, doc *niceyaml.Node) {
				t.Helper()

				type tagged struct {
					Labels map[string]string `yaml:"labels"`
					Max    int               `yaml:"max"`
				}

				type flat struct {
					Arr  [2]tagged `yaml:"arr"`
					Name string    `yaml:"name"`
				}

				type with struct {
					Extra *map[string][]flat `yaml:",inline"`
					More  []flat             `yaml:"more"`
				}

				calls := 0

				got, err := doc.Decode[struct {
					Ws []with `yaml:"ws"`
				}](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(g *tagged) {
						calls++
						g.Labels = map[string]string{}
					}),
				))
				require.NoError(t, err)
				require.Len(t, got.Ws, 1)
				require.NotNil(t, got.Ws[0].Extra)
				require.Len(t, (*got.Ws[0].Extra)["items"], 3)
				require.Len(t, got.Ws[0].More, 1)

				assert.Equal(t, "m", got.Ws[0].More[0].Name)
				assert.Equal(t, "x", (*got.Ws[0].Extra)["items"][1].Name)
				assert.Equal(t, 2, calls)
			},
		},
	}

	refs := yamltest.References(stringtest.LinesLF(
		"bar: &bar {host: h}",
		"fix: &fix {fixed: {codes: [4]}}",
		"lst: &lst {more: [{name: m}]}",
	))

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, yamltest.FirstDocument(t, tc.input, refs))
		})
	}
}

func TestNode_Decode_DefaultsOfInlineFieldsBesideReferenceMerges(t *testing.T) {
	t.Parallel()

	type hook struct {
		Codes []int `yaml:"codes"`
		Max   int   `yaml:"max"`
	}

	type inl struct {
		Any  any      `yaml:"any"`
		Lead *hook    `yaml:"lead"`
		Tags []string `yaml:"tags"`
	}

	hookDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(h *hook) { h.Max = 3 }))

	refs := yamltest.References(stringtest.LinesLF(
		"n: &n {name: ref}",
		"extra: &extra {more: {codes: [9]}}",
		"lead: &lead {lead: {codes: [9]}}",
		"num: &num {num: 4}",
	))

	read := func(t *testing.T, input string) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocument(t, input, refs)
	}

	// Each struct in the list merges the reference document. The decoder
	// starts each struct or map held inline from its defaults, alone or
	// behind a pointer, and each value that it creates inside from the
	// defaults of its type.
	tcs := map[string]struct {
		test func(t *testing.T)
	}{
		"struct held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// The decoder reads the list before the place of the anchor
				// that the alias reads, so it gives the alias a value of its
				// own.
				type box struct {
					Inl  inl    `yaml:",inline"`
					Name string `yaml:"name"`
				}

				doc := read(t, "late: &l {codes: [1]}\nboxes: [{<<: *n, any: *l}]\n")

				for _, opts := range [][]niceyaml.DecodeOption{nil, {hookDefaults}} {
					got, err := doc.Decode[struct {
						Boxes []box `yaml:"boxes"`
						Late  hook  `yaml:"late"`
					}](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 1)

					assert.IsType(t, map[string]any{}, got.Boxes[0].Inl.Any)
					assert.Equal(t, "ref", got.Boxes[0].Name)
				}
			},
		},
		"struct held inline inside a struct held inline": {
			test: func(t *testing.T) {
				t.Helper()

				type mid struct {
					Inner inl `yaml:",inline"`
				}

				type box struct {
					Mid  mid    `yaml:",inline"`
					Name string `yaml:"name"`
				}

				type boxes struct {
					Boxes []box `yaml:"boxes"`
					Late  hook  `yaml:"late"`
				}

				// The decoder reads the list before the place of the anchor
				// that the alias reads, so it gives the alias a value of its
				// own.
				doc := read(t, "late: &l {codes: [1]}\nboxes: [{<<: *n, any: *l}]\n")

				for _, opts := range [][]niceyaml.DecodeOption{nil, {hookDefaults}} {
					got, err := doc.Decode[boxes](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 1)

					assert.IsType(t, map[string]any{}, got.Boxes[0].Mid.Inner.Any)
					assert.Equal(t, "ref", got.Boxes[0].Name)
				}

				// The merge key brings in the lead, which starts from the
				// target that the function sets.
				doc = read(t, "boxes: [{<<: *lead, any: 1}]\n")

				got, err := doc.Decode[boxes](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(b *box) { b.Mid.Inner.Lead = &hook{Max: 7} }),
				))
				require.NoError(t, err)
				require.Len(t, got.Boxes, 1)

				assert.Equal(t, uint64(1), got.Boxes[0].Mid.Inner.Any)
				assert.Equal(t, &hook{Codes: []int{9}, Max: 7}, got.Boxes[0].Mid.Inner.Lead)
			},
		},
		"field of a struct held inline that the box names too": {
			test: func(t *testing.T) {
				t.Helper()

				// The box names the field too, so the field keeps its start.
				type named struct {
					Name string `yaml:"name"`
					Host string `yaml:"host"`
				}

				type box struct {
					Inl  named  `yaml:",inline"`
					Name string `yaml:"name"`
				}

				type pbox struct {
					Inl  *named `yaml:",inline"`
					Name string `yaml:"name"`
				}

				doc := read(t, "boxes: [{<<: *n}]\n")

				got, err := doc.Decode[struct {
					Boxes []box `yaml:"boxes"`
				}](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(b *box) { b.Inl.Name = "keep" }),
				))
				require.NoError(t, err)
				require.Len(t, got.Boxes, 1)

				assert.Equal(t, "keep", got.Boxes[0].Inl.Name)
				assert.Equal(t, "ref", got.Boxes[0].Name)

				pgot, err := doc.Decode[struct {
					Boxes []pbox `yaml:"boxes"`
				}](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(b *pbox) { b.Inl = &named{Name: "keep"} }),
				))
				require.NoError(t, err)
				require.Len(t, pgot.Boxes, 1)
				require.NotNil(t, pgot.Boxes[0].Inl)

				assert.Equal(t, "keep", pgot.Boxes[0].Inl.Name)
				assert.Equal(t, "ref", pgot.Boxes[0].Name)
			},
		},
		"field that decodes itself inside a struct held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// The field starts from its defaults, as it does without the
				// merge key.
				type selfInl struct {
					Self defaultsCounted `yaml:"self"`
				}

				type box struct {
					Inl  selfInl `yaml:",inline"`
					Name string  `yaml:"name"`
				}

				doc := read(t, "boxes: [{<<: *n, self: x}]\n")

				got, err := doc.Decode[struct {
					Boxes []box `yaml:"boxes"`
				}](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(c *defaultsCounted) { c.Port = 5 }),
				))
				require.NoError(t, err)
				require.Len(t, got.Boxes, 1)

				assert.Equal(t, "x", got.Boxes[0].Inl.Self.Name)
				assert.Equal(t, 5, got.Boxes[0].Inl.Self.Port)
				assert.Equal(t, "ref", got.Boxes[0].Name)
			},
		},
		"field that decodes itself inside a pointer to a struct held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// The field starts from the target that the function sets.
				type selfInl struct {
					Self defaultsCounted `yaml:"self"`
				}

				type box struct {
					Inl  *selfInl `yaml:",inline"`
					Name string   `yaml:"name"`
				}

				doc := read(t, "boxes: [{<<: *n, self: x}]\n")

				boxDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *box) {
					b.Inl = &selfInl{Self: defaultsCounted{Port: 5}}
				}))

				for _, decode := range []struct {
					opts []niceyaml.DecodeOption
					port int
				}{
					{},
					{opts: []niceyaml.DecodeOption{boxDefaults}, port: 5},
				} {
					got, err := doc.Decode[struct {
						Boxes []box `yaml:"boxes"`
					}](t.Context(), decode.opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 1)
					require.NotNil(t, got.Boxes[0].Inl)

					assert.Equal(t, "x", got.Boxes[0].Inl.Self.Name)
					assert.Equal(t, decode.port, got.Boxes[0].Inl.Self.Port)
					assert.Equal(t, "ref", got.Boxes[0].Name)
				}
			},
		},
		"pointer to a struct held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// The decoder reads the list before the place of the anchor
				// that each alias reads, so it gives each alias a value of
				// its own.
				type box struct {
					Inl  *inl   `yaml:",inline"`
					Name string `yaml:"name"`
				}

				type boxes struct {
					Boxes []box `yaml:"boxes"`
					Late  hook  `yaml:"late"`
				}

				doc := read(t, "late: &l {codes: [1]}\nboxes: [{<<: *n, any: *l}]\n")

				for _, opts := range [][]niceyaml.DecodeOption{nil, {hookDefaults}} {
					got, err := doc.Decode[boxes](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 1)
					require.NotNil(t, got.Boxes[0].Inl)

					assert.IsType(t, map[string]any{}, got.Boxes[0].Inl.Any)
					assert.Equal(t, "ref", got.Boxes[0].Name)
				}

				// The decoder reads the list before the place of the anchor,
				// so it reads the anchor again for each alias, and the two
				// aliases hold values of their own.
				doc = read(t, "late: &l {codes: [1]}\nboxes: [{<<: *n, lead: *l}, {<<: *n, lead: *l}]\n")

				for _, opts := range [][]niceyaml.DecodeOption{nil, {hookDefaults}} {
					got, err := doc.Decode[boxes](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 2)
					require.NotNil(t, got.Boxes[0].Inl)
					require.NotNil(t, got.Boxes[1].Inl)
					require.NotNil(t, got.Boxes[0].Inl.Lead)
					require.NotNil(t, got.Boxes[1].Inl.Lead)

					assert.Equal(t, []int{1}, got.Boxes[0].Inl.Lead.Codes)
					assert.Equal(t, []int{1}, got.Boxes[1].Inl.Lead.Codes)
					assert.NotEqual(t,
						reflect.ValueOf(got.Boxes[0].Inl.Lead.Codes).Pointer(),
						reflect.ValueOf(got.Boxes[1].Inl.Lead.Codes).Pointer())
				}

				// The target of the lead starts from the defaults of its type.
				doc = read(t, "late: &l {codes: [1]}\nboxes: [{<<: *n, any: *l, lead: {codes: [2]}}]\n")

				got, err := doc.Decode[boxes](t.Context(), hookDefaults)
				require.NoError(t, err)
				require.Len(t, got.Boxes, 1)
				require.NotNil(t, got.Boxes[0].Inl)

				assert.IsType(t, map[string]any{}, got.Boxes[0].Inl.Any)
				assert.Equal(t, &hook{Codes: []int{2}, Max: 3}, got.Boxes[0].Inl.Lead)

				// The merge key brings in the lead, which starts from the
				// target that the function sets.
				doc = read(t, "boxes: [{<<: *lead, any: 1}]\n")

				got, err = doc.Decode[boxes](t.Context(), niceyaml.OverrideTypeRules(
					niceyaml.TypeDefaults(func(b *box) { b.Inl = &inl{Lead: &hook{Max: 7}} }),
				))
				require.NoError(t, err)
				require.Len(t, got.Boxes, 1)
				require.NotNil(t, got.Boxes[0].Inl)

				assert.Equal(t, uint64(1), got.Boxes[0].Inl.Any)
				assert.Equal(t, &hook{Codes: []int{9}, Max: 7}, got.Boxes[0].Inl.Lead)
			},
		},
		"struct that decodes itself held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// The method of the struct reads the whole mapping, and the
				// decoder calls it once, on the defaults.
				type box struct {
					Self defaultsSelf `yaml:",inline"`
					Num  int          `yaml:"num"`
				}

				doc := read(t, "boxes: [{<<: *num, k: 1}]\n")

				calls := 0
				selfDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(s *defaultsSelf) {
					s.Calls = &calls
					s.Port = 5
				}))

				for _, decode := range []struct {
					opts  []niceyaml.DecodeOption
					port  int
					calls int
				}{
					{},
					{opts: []niceyaml.DecodeOption{selfDefaults}, port: 5, calls: 1},
				} {
					got, err := doc.Decode[struct {
						Boxes []box `yaml:"boxes"`
					}](t.Context(), decode.opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 1)

					assert.Equal(t, map[string]int{"k": 1, "num": 4}, got.Boxes[0].Self.M)
					assert.Equal(t, decode.port, got.Boxes[0].Self.Port)
					assert.Equal(t, 4, got.Boxes[0].Num)
					assert.Equal(t, decode.calls, calls)
				}
			},
		},
		"pointer to a struct that decodes itself held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// A function of TypeDefaults of the box sets the target that
				// the decoder starts from, and the decoder calls the method
				// once.
				type box struct {
					Self *defaultsSelf `yaml:",inline"`
					Num  int           `yaml:"num"`
				}

				doc := read(t, "boxes: [{<<: *num, k: 1}]\n")

				calls := 0
				boxDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *box) {
					b.Self = &defaultsSelf{Calls: &calls, Port: 5}
				}))

				for _, decode := range []struct {
					opts  []niceyaml.DecodeOption
					port  int
					calls int
				}{
					{},
					{opts: []niceyaml.DecodeOption{boxDefaults}, port: 5, calls: 1},
				} {
					got, err := doc.Decode[struct {
						Boxes []box `yaml:"boxes"`
					}](t.Context(), decode.opts...)
					require.NoError(t, err)
					require.Len(t, got.Boxes, 1)
					require.NotNil(t, got.Boxes[0].Self)

					assert.Equal(t, map[string]int{"k": 1, "num": 4}, got.Boxes[0].Self.M)
					assert.Equal(t, decode.port, got.Boxes[0].Self.Port)
					assert.Equal(t, 4, got.Boxes[0].Num)
					assert.Equal(t, decode.calls, calls)
				}
			},
		},
		"map held inline": {
			test: func(t *testing.T) {
				t.Helper()

				// The decoder starts each value of the map from the defaults
				// of its type, the value that the merge key brings in
				// included.
				type bag struct {
					Rest map[string]hook `yaml:",inline"`
					More hook            `yaml:"more"`
				}

				doc := read(t, "bags: [{<<: *extra, own: {codes: [2]}}]\n")

				for _, decode := range []struct {
					opts []niceyaml.DecodeOption
					max  int
				}{
					{},
					{opts: []niceyaml.DecodeOption{hookDefaults}, max: 3},
				} {
					got, err := doc.Decode[struct {
						Bags []bag `yaml:"bags"`
					}](t.Context(), decode.opts...)
					require.NoError(t, err)
					require.Len(t, got.Bags, 1)

					assert.Equal(t, map[string]hook{
						"more": {Codes: []int{9}, Max: decode.max},
						"own":  {Codes: []int{2}, Max: decode.max},
					}, got.Bags[0].Rest)
				}

				// The decoder reads the list before the place of the anchor
				// that each alias reads, so it gives each alias a value of
				// its own.
				doc = read(t, "late: &l {codes: [1]}\nbags: [{<<: *extra, own: *l, two: *l}]\n")

				for _, opts := range [][]niceyaml.DecodeOption{nil, {hookDefaults}} {
					got, err := doc.Decode[struct {
						Bags []bag `yaml:"bags"`
						Late hook  `yaml:"late"`
					}](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.Bags, 1)

					own, two := got.Bags[0].Rest["own"], got.Bags[0].Rest["two"]
					assert.Equal(t, []int{1}, own.Codes)
					assert.Equal(t, []int{1}, two.Codes)
					assert.NotEqual(t, reflect.ValueOf(own.Codes).Pointer(), reflect.ValueOf(two.Codes).Pointer())
				}

				// The decoder reads the list before the place of the anchor
				// that the alias reads, so it gives the alias a value of its
				// own.
				type anyBag struct {
					Rest map[string]any `yaml:",inline"`
					Name string         `yaml:"name"`
				}

				doc = read(t, "late: &l {codes: [1]}\nbags: [{<<: *n, own: *l}]\n")

				bagDefaults := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *anyBag) { b.Name = "d" }))

				for _, opts := range [][]niceyaml.DecodeOption{nil, {bagDefaults}} {
					got, err := doc.Decode[struct {
						Bags []anyBag `yaml:"bags"`
						Late hook     `yaml:"late"`
					}](t.Context(), opts...)
					require.NoError(t, err)
					require.Len(t, got.Bags, 1)

					assert.IsType(t, map[string]any{}, got.Bags[0].Rest["own"])
					assert.Equal(t, "ref", got.Bags[0].Name)
				}
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.test(t)
		})
	}
}

func TestNode_Decode_DefaultsCalls(t *testing.T) {
	t.Parallel()

	type leaf struct {
		Max int `yaml:"max"`
	}

	// The function levels returns a list of the given count of levels
	// after the first.
	// Each level lists the level before it twice through aliases. With
	// merge set, each mapping merges the anchor bar of a reference
	// document first.
	levels := func(t *testing.T, count int, merge bool) *niceyaml.Node {
		t.Helper()

		prefix := ""
		opts := []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)}

		if merge {
			prefix = "<<: *bar, "

			opts = append(opts, yamltest.References("bar: &bar {host: h}\n"))
		}

		lines := []string{"levels:", "- &n0 {" + prefix + "name: n0}"}
		for i := 1; i <= count; i++ {
			lines = append(lines, fmt.Sprintf("- &n%d {%sname: n%d, kids: [*n%d, *n%d]}", i, prefix, i, i-1, i-1))
		}

		return yamltest.FirstDocument(t, stringtest.LinesLF(lines...), opts...)
	}

	// The function countLeaf returns a rule that counts each call of the
	// function of leaf in calls.
	countLeaf := func(calls *int) niceyaml.DecodeOption {
		return niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*leaf) { *calls++ }))
	}

	// The function countSelf returns a rule that gives each
	// defaultsCounted the counter calls.
	countSelf := func(calls *int) niceyaml.DecodeOption {
		return niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(c *defaultsCounted) {
			c.Calls = calls
			c.Port = 5
		}))
	}

	// Each row returns the count of calls of a function of TypeDefaults or
	// of an unmarshaler. The count grows with the values that the decoder
	// creates, not with the document as its aliases write it out.
	tcs := map[string]struct {
		calls func(t *testing.T) int
		want  int
	}{
		"pointer kids": {
			want: 21,
			calls: func(t *testing.T) int {
				t.Helper()

				type node struct {
					Kids []*node `yaml:"kids"`
					Name string  `yaml:"name"`
					Max  int     `yaml:"max"`
				}

				calls := 0

				got, err := levels(t, 20, false).Decode[struct {
					Levels []*node `yaml:"levels"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*node) { calls++ })))
				require.NoError(t, err)
				require.Len(t, got.Levels, 21)
				require.Len(t, got.Levels[20].Kids, 2)

				assert.Same(t, got.Levels[20].Kids[0], got.Levels[20].Kids[1])

				return calls
			},
		},
		"struct kids": {
			want: 21,
			calls: func(t *testing.T) int {
				t.Helper()

				type val struct {
					Kids []val  `yaml:"kids"`
					Name string `yaml:"name"`
					Host string `yaml:"host"`
					Max  int    `yaml:"max"`
				}

				calls := 0

				_, err := levels(t, 20, false).Decode[struct {
					Levels []val `yaml:"levels"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*val) { calls++ })))
				require.NoError(t, err)

				return calls
			},
		},
		// The decoder creates a value for each alias of n0 and n1, and
		// each of those values starts from its defaults.
		"anchors that the type does not read": {
			want: 13,
			calls: func(t *testing.T) int {
				t.Helper()

				type node struct {
					Kids []*node `yaml:"kids"`
					Name string  `yaml:"name"`
					Max  int     `yaml:"max"`
				}

				doc := yamltest.FirstDocument(t, stringtest.LinesLF(
					"x-levels: [&n0 {name: n0}, &n1 {name: n1, kids: [*n0, *n0, *n0]}]",
					"top: [{name: top, kids: [*n1, *n1, *n1]}]",
				))

				calls := 0

				got, err := doc.Decode[struct {
					Top []*node `yaml:"top"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*node) { calls++ })))
				require.NoError(t, err)
				require.Len(t, got.Top, 1)
				require.Len(t, got.Top[0].Kids, 3)

				// The decoder gives each kid a target of its own.
				kids := got.Top[0].Kids
				assert.NotSame(t, kids[0], kids[1])
				assert.NotSame(t, kids[0], kids[2])
				assert.NotSame(t, kids[1], kids[2])

				return calls
			},
		},
		// Each element starts from the defaults of its type, so each alias
		// takes the value of its anchor, although the function gives each
		// call another value.
		"struct kids with the merge": {
			want: 13,
			calls: func(t *testing.T) int {
				t.Helper()

				type val struct {
					Kids []val  `yaml:"kids"`
					Name string `yaml:"name"`
					Host string `yaml:"host"`
					Max  int    `yaml:"max"`
				}

				calls := 0

				got, err := levels(t, 12, true).Decode[struct {
					Levels []val `yaml:"levels"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(v *val) {
					calls++
					v.Max = calls
				})))
				require.NoError(t, err)
				require.Len(t, got.Levels, 13)
				assert.Equal(t, "h", got.Levels[12].Host)

				return calls
			},
		},
		// The kids sit in a struct held inline, and they take the values
		// of their anchors, as the kids of a named field do.
		"inline kids with the merge": {
			want: 13,
			calls: func(t *testing.T) int {
				t.Helper()

				type inl struct {
					Body struct {
						Kids []inl `yaml:"kids"`
					} `yaml:",inline"`
					Name string `yaml:"name"`
					Host string `yaml:"host"`
					Max  int    `yaml:"max"`
				}

				calls := 0

				got, err := levels(t, 12, true).Decode[struct {
					Levels []inl `yaml:"levels"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(v *inl) {
					calls++
					v.Max = calls
				})))
				require.NoError(t, err)
				require.Len(t, got.Levels, 13)
				require.Len(t, got.Levels[12].Body.Kids, 2)
				assert.Equal(t, "n11", got.Levels[12].Body.Kids[0].Name)

				return calls
			},
		},
		// The kids sit in a struct held inline inside another struct held
		// inline, and they take the values of their anchors too.
		"nested inline kids with the merge": {
			want: 13,
			calls: func(t *testing.T) int {
				t.Helper()

				type inl struct {
					Body struct {
						Deep struct {
							Kids []inl `yaml:"kids"`
						} `yaml:",inline"`
					} `yaml:",inline"`
					Name string `yaml:"name"`
					Host string `yaml:"host"`
					Max  int    `yaml:"max"`
				}

				calls := 0

				got, err := levels(t, 12, true).Decode[struct {
					Levels []inl `yaml:"levels"`
				}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(v *inl) {
					calls++
					v.Max = calls
				})))
				require.NoError(t, err)
				require.Len(t, got.Levels, 13)
				require.Len(t, got.Levels[12].Body.Deep.Kids, 2)
				assert.Equal(t, "n11", got.Levels[12].Body.Deep.Kids[0].Name)

				return calls
			},
		},
		"copies of a value that decodes itself": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				calls := 0

				got := defaultsAliasList[defaultsSelf](t, "{k: 1}",
					niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(s *defaultsSelf) {
						s.Calls = &calls
						s.Port = 2
					})))

				for _, v := range got {
					assert.Equal(t, 2, v.Port)
					assert.Equal(t, map[string]int{"k": 1}, v.M)
				}

				return calls
			},
		},
		// The decoder reads the anchor of the reference document again for
		// each alias, so each element of refs calls the method once.
		"lists of a value that decodes itself": {
			want: 5,
			calls: func(t *testing.T) int {
				t.Helper()

				doc := yamltest.FirstDocument(t, stringtest.LinesLF(
					"vals: [&a x, *a, *a]",
					"ptrs: [&b y, *b, *b]",
					"refs: [*r, *r, *r]",
				), yamltest.References("r: &r z\n"))

				calls := 0

				got, err := doc.Decode[struct {
					Vals []defaultsCounted  `yaml:"vals"`
					Ptrs []*defaultsCounted `yaml:"ptrs"`
					Refs []defaultsCounted  `yaml:"refs"`
				}](t.Context(), countSelf(&calls))
				require.NoError(t, err)
				require.Len(t, got.Vals, 3)
				require.Len(t, got.Ptrs, 3)
				require.Len(t, got.Refs, 3)
				assert.Equal(t, 5, got.Vals[2].Port)
				assert.Equal(t, "y", got.Ptrs[2].Name)
				assert.Equal(t, 5, got.Refs[2].Port)

				return calls
			},
		},
		"string type that decodes itself": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				before := defaultsLogLevelCalls.Load()

				got := defaultsAliasList[defaultsLogLevel](t, "debug",
					niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(l *defaultsLogLevel) { *l = "info" })))
				assert.Equal(t, []defaultsLogLevel{"debug", "debug", "debug"}, got)

				return int(defaultsLogLevelCalls.Load() - before)
			},
		},
		// The value of the place holds the map that its start holds, and
		// the place is not a field of a struct.
		"map type that decodes itself into its start": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				before := defaultsLabelsCalls.Load()

				got := defaultsAliasList[defaultsLabels](t, "{d: x}",
					niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(l *defaultsLabels) {
						*l = defaultsLabels{"d": "x"}
					})))

				for _, v := range got {
					assert.Equal(t, defaultsLabels{"d": "x"}, v)
				}

				return int(defaultsLabelsCalls.Load() - before)
			},
		},
		"struct of leaves in an array": {
			want: 2,
			calls: func(t *testing.T) int {
				t.Helper()

				type flat struct {
					Arr  [2]leaf `yaml:"arr"`
					Name string  `yaml:"name"`
				}

				calls := 0
				got := defaultsAliasList[flat](t, "{arr: [{max: 1}, {}], name: x}", countLeaf(&calls))
				assert.Equal(t, flat{Arr: [2]leaf{{Max: 1}}, Name: "x"}, got[2])

				return calls
			},
		},
		"values without size": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				type empty struct{}

				type zero struct {
					Env  map[string]string `yaml:"env"`
					E    []empty           `yaml:"e"`
					P    *empty            `yaml:"p"`
					Leaf [1]leaf           `yaml:"leaf"`
				}

				calls := 0

				got := defaultsAliasList[zero](t, "{e: [{}], p: {}, leaf: [{}]}", countLeaf(&calls),
					niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(z *zero) { z.Env = map[string]string{} })))
				// The aliases take the value of the anchor, with the map
				// that its defaults set.
				require.NotNil(t, got[0].Env)
				assert.Equal(t, reflect.ValueOf(got[0].Env).Pointer(), reflect.ValueOf(got[1].Env).Pointer())

				return calls
			},
		},
		// Each place below reads a list of one anchor and two aliases of
		// it.
		"pointer place": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				calls := 0
				got := defaultsAliasList[*leaf](t, "{max: 1}", countLeaf(&calls))
				assert.Equal(t, &leaf{Max: 1}, got[2])

				return calls
			},
		},
		"map place": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				calls := 0
				got := defaultsAliasList[map[string]leaf](t, "{k0: {max: 1}}", countLeaf(&calls))
				assert.Equal(t, map[string]leaf{"k0": {Max: 1}}, got[2])

				return calls
			},
		},
		"slice place": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				calls := 0
				got := defaultsAliasList[[]leaf](t, "[{max: 1}]", countLeaf(&calls))
				assert.Equal(t, []leaf{{Max: 1}}, got[2])

				return calls
			},
		},
		"struct place": {
			want: 2,
			calls: func(t *testing.T) int {
				t.Helper()

				type bag struct {
					Rest map[string]leaf `yaml:",inline"`
					Name string          `yaml:"name"`
				}

				calls := 0
				got := defaultsAliasList[bag](t, "{k0: {max: 1}, k1: {max: 1}}", countLeaf(&calls))
				assert.Equal(t, map[string]leaf{"k0": {Max: 1}, "k1": {Max: 1}}, got[2].Rest)

				return calls
			},
		},
		// The box holds an int in an interface value, which holds no
		// memory, so the aliases take the value built for the anchor.
		"value that decodes itself with an int in a box": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				calls := 0

				got := defaultsAliasList[defaultsBoxed](t, "x",
					niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(b *defaultsBoxed) {
						b.Calls = &calls
						b.Box[0] = 500
					})))

				for _, v := range got {
					assert.Equal(t, "x", v.Name)
					assert.Equal(t, [1]any{500}, v.Box)
				}

				return calls
			},
		},
		// Each struct below holds a start field inside a struct field.
		"start field beside an interface value": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				type wrap struct {
					Meta any             `yaml:"meta"`
					Self defaultsCounted `yaml:"self"`
				}

				calls := 0
				got := defaultsAliasList[wrap](t, "{meta: {k: v}, self: x}", countSelf(&calls))
				assert.Equal(t, "x", got[2].Self.Name)
				assert.Equal(t, 5, got[2].Self.Port)

				return calls
			},
		},
		"start field beside an array": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				type wrap struct {
					Arr  [1][]int        `yaml:"arr"`
					Self defaultsCounted `yaml:"self"`
				}

				calls := 0
				got := defaultsAliasList[wrap](t, "{arr: [[1]], self: y}", countSelf(&calls))
				assert.Equal(t, [1][]int{{1}}, got[2].Arr)
				assert.Equal(t, "y", got[2].Self.Name)

				return calls
			},
		},
		"start field beside no memory": {
			want: 1,
			calls: func(t *testing.T) int {
				t.Helper()

				type wrap struct {
					Name string          `yaml:"name"`
					Self defaultsCounted `yaml:"self"`
				}

				calls := 0
				got := defaultsAliasList[wrap](t, "{name: n, self: z}", countSelf(&calls))
				assert.Equal(t, "n", got[2].Name)
				assert.Equal(t, "z", got[2].Self.Name)

				return calls
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.calls(t))
		})
	}
}

func TestNode_Decode_DefaultsInline(t *testing.T) {
	t.Parallel()

	type Common struct {
		Name string `yaml:"name"`
		Tier int    `yaml:"tier"`
	}

	type item struct {
		Common `yaml:",inline"`

		Name string `yaml:"name"`
	}

	opts := niceyaml.DecodeOptions(
		niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(c *Common) {
			c.Name = "inner"
			c.Tier = 2
		})),
	)

	doc := yamltest.FirstDocument(t, "- {name: a}\n- {tier: 3}\n")

	got, err := doc.Decode[[]item](t.Context(), opts)
	require.NoError(t, err)

	// The field the outer struct also names keeps its default, where the
	// decoder sets it to zero.
	assert.Equal(t, []item{
		{Common: Common{Name: "inner", Tier: 2}, Name: "a"},
		{Common: Common{Name: "inner", Tier: 3}},
	}, got)
}

// defaultsLevel decodes itself, and keeps the default it starts from for
// a document that names no level.
type defaultsLevel struct {
	Name  string
	Level int
}

// UnmarshalYAML decodes a name, and keeps the level it holds.
func (l *defaultsLevel) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	return decode(&l.Name)
}

// defaultsCounted decodes itself, and counts each call of its method in
// the int that Calls points to. It keeps the port it holds.
type defaultsCounted struct {
	Calls *int
	Name  string
	Port  int
}

// UnmarshalYAML counts the call when Calls is not nil, and decodes a name.
func (c *defaultsCounted) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	if c.Calls != nil {
		*c.Calls++
	}

	return decode(&c.Name)
}

// defaultsBoxed decodes itself, and counts each call of its method in the
// int that Calls points to. It keeps the value it holds in its box.
type defaultsBoxed struct {
	Calls *int
	Box   [1]any
	Name  string
}

// UnmarshalYAML counts the call when Calls is not nil, and decodes a name.
func (b *defaultsBoxed) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	if b.Calls != nil {
		*b.Calls++
	}

	return decode(&b.Name)
}

// defaultsSelf decodes itself, and counts each call of its method in the
// int that Calls points to. It keeps the port it holds.
type defaultsSelf struct {
	Calls *int
	M     map[string]int
	Port  int
}

// UnmarshalYAML counts the call when Calls is not nil, and decodes a map.
func (s *defaultsSelf) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	if s.Calls != nil {
		*s.Calls++
	}

	return decode(&s.M)
}

// defaultsLabels is a map of labels that decodes itself.
type defaultsLabels map[string]string

// UnmarshalYAML counts the call in [defaultsLabelsCalls] when l is not
// nil. It decodes a map of labels and copies the labels into the map that
// l holds. It makes that map first when l is nil.
func (l *defaultsLabels) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	if *l != nil {
		defaultsLabelsCalls.Add(1)
	}

	var labels map[string]string

	err := decode(&labels)
	if err != nil {
		return err
	}

	if *l == nil {
		*l = defaultsLabels{}
	}

	maps.Copy(*l, labels)

	return nil
}

// defaultsBag is a bag of values that decodes itself.
type defaultsBag map[string]any

// UnmarshalYAML decodes a mapping and copies its entries into the map that
// b holds. It makes that map first when b is nil. It then adds one to the
// int under the key "calls", so the map counts the calls that wrote it.
func (b *defaultsBag) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	var entries map[string]any

	err := decode(&entries)
	if err != nil {
		return err
	}

	if *b == nil {
		*b = defaultsBag{}
	}

	maps.Copy(*b, entries)

	if calls, ok := (*b)["calls"].(int); ok {
		(*b)["calls"] = calls + 1
	} else {
		(*b)["calls"] = 1
	}

	return nil
}

// defaultsTally decodes itself, and counts each call of its method in the
// first element of Calls.
type defaultsTally struct {
	Calls []int
	Name  string
}

// UnmarshalYAML decodes a name, and adds one to the first element of
// Calls when Calls holds one.
func (c *defaultsTally) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	if len(c.Calls) > 0 {
		c.Calls[0]++
	}

	return decode(&c.Name)
}

// defaultsLogLevel is a level of logs that decodes itself.
type defaultsLogLevel string

// UnmarshalYAML counts the call in [defaultsLogLevelCalls] when l is not
// empty, and decodes a string.
func (l *defaultsLogLevel) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	if *l != "" {
		defaultsLogLevelCalls.Add(1)
	}

	return decode((*string)(l))
}

// defaultsAliasList decodes a list of one anchor of content and two
// aliases of it as a list of T, under opts.
func defaultsAliasList[T any](t *testing.T, content string, opts ...niceyaml.DecodeOption) []T {
	t.Helper()

	got, err := yamltest.FirstDocument(t, "vals: [&a "+content+", *a, *a]\n").Decode[struct {
		Vals []T `yaml:"vals"`
	}](t.Context(), opts...)
	require.NoError(t, err)
	require.Len(t, got.Vals, 3)

	return got.Vals
}

// defaultsInArrays decodes the list vals of doc, where each element holds
// one T in the array arr, and a name. The function fn sets the defaults
// of T. The result holds the T of each element.
func defaultsInArrays[T any](t *testing.T, doc *niceyaml.Node, fn func(*T)) []T {
	t.Helper()

	got, err := doc.Decode[struct {
		Vals []struct {
			Arr  [1]T   `yaml:"arr"`
			Name string `yaml:"name"`
		} `yaml:"vals"`
	}](t.Context(), niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(fn)))
	require.NoError(t, err)
	require.Len(t, got.Vals, 3)

	vals := make([]T, 0, len(got.Vals))
	for _, v := range got.Vals {
		vals = append(vals, v.Arr[0])
	}

	return vals
}

func TestNode_DecodeInto_DecodesTargetOfPointerOnce(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "counted: kaffe\n")

	// The decoder starts the target from the one the caller set, so the
	// value decodes itself once.
	calls := 0
	held := struct {
		Counted *defaultsCounted `yaml:"counted"`
	}{Counted: &defaultsCounted{Calls: &calls}}

	require.NoError(t, doc.DecodeInto(t.Context(), &held))
	assert.Equal(t, "kaffe", held.Counted.Name)
	assert.Equal(t, 1, calls)
}

func TestNode_Decode_DefaultsOfValueThatDecodesItself(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "[info, debug]\n")

	got, err := doc.Decode[[]defaultsLevel](t.Context(),
		niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(l *defaultsLevel) { l.Level = 3 })))
	require.NoError(t, err)
	assert.Equal(t, []defaultsLevel{{Name: "info", Level: 3}, {Name: "debug", Level: 3}}, got)
}

// defaultsWrapper decodes itself into the printer that Printer points to,
// which is nil when the method starts.
type defaultsWrapper struct {
	Printer *defaultsPrinter
}

// UnmarshalYAML decodes a printer into the nil pointer Printer.
func (w *defaultsWrapper) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	return decode(&w.Printer)
}

func TestNode_Decode_DefaultsOfPointerTargetInMethod(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "w: {host: a}\n")

	// The method decodes into a nil pointer, so the decoder creates the
	// printer, and the printer starts from its defaults.
	got, err := doc.Decode[struct {
		W defaultsWrapper `yaml:"w"`
	}](t.Context(), defaultsDecoding)
	require.NoError(t, err)
	assert.Equal(t, &defaultsPrinter{Host: "a", Port: 9100}, got.W.Printer)
}

// defaultsKey is a map key that decodes its name from text and keeps the
// unit that it starts from.
type defaultsKey struct{ Name, Unit string }

func (k *defaultsKey) UnmarshalText(b []byte) error {
	k.Name = string(b)

	return nil
}

func TestNode_Decode_DefaultsOfMapKeys(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "small: 1\n")

	got, err := doc.Decode[map[defaultsKey]int](t.Context(),
		niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(k *defaultsKey) { k.Unit = "ml" })))
	require.NoError(t, err)
	assert.Equal(t, map[defaultsKey]int{{Name: "small", Unit: "ml"}: 1}, got)
}

// defaultsPort fails Validate for a port greater than 5.
type defaultsPort struct {
	Port int `yaml:"port"`
}

func (p defaultsPort) Validate() error {
	if p.Port > 5 {
		return errors.New("port too big")
	}

	return nil
}

func TestNode_Decode_DefaultsOfMapKeysInPaths(t *testing.T) {
	t.Parallel()

	// The path of an error below a key spells the key as the document does
	// when the key starts from its defaults.
	doc := yamltest.FirstDocument(t, "small: {port: 9}\nbig: {port: 1}\n")
	keys := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(k *defaultsKey) { k.Unit = "ml" }))

	_, err := doc.Decode[map[defaultsKey]defaultsPort](t.Context(), keys)
	require.ErrorContains(t, err, "$.small: port too big")

	v := map[defaultsKey]defaultsPort{{Name: "small", Unit: "ml"}: {Port: 9}}
	err = doc.SelfValidate(t.Context(), &v, keys)
	require.ErrorContains(t, err, "$.small: port too big")

	// When the defaults of the key panic, the walk cannot decode the key,
	// and the path spells the key as the value that the map holds.
	boom := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*defaultsKey) { panic("boom") }))
	v = map[defaultsKey]defaultsPort{{Name: "small"}: {Port: 9}}
	err = doc.SelfValidate(t.Context(), &v, boom)
	require.ErrorContains(t, err, "$.'{small }': port too big")
	require.NotErrorAs(t, err, new(*niceyaml.PanicError))
}

func TestNode_Decode_DefaultsPanic(t *testing.T) {
	t.Parallel()

	boom := niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*defaultsPrinter) { panic("boom") }))

	t.Run("a value the decode creates", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "stations: [{host: a}]\n")

		_, err := doc.Decode[defaultsCafe](
			t.Context(),
			niceyaml.OverrideTypeRules(niceyaml.TypeDefaults(func(*defaultsCafe) {})),
			boom,
		)
		require.ErrorAs(t, err, new(*niceyaml.PanicError))
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("the root", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "host: a\n")

		_, err := doc.Decode[defaultsPrinter](t.Context(), boom)
		require.ErrorAs(t, err, new(*niceyaml.PanicError))
	})

	t.Run("an element", func(t *testing.T) {
		t.Parallel()

		// The decoder asks for the defaults of the element, and the error
		// names the type of the element at the node of the element.
		doc := yamltest.FirstDocument(t, "- {host: a}\n")

		_, err := doc.Decode[[]defaultsPrinter](t.Context(), boom)
		require.ErrorAs(t, err, new(*niceyaml.PanicError))
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
		assert.ErrorContains(t, err, "1:3: the defaults of niceyaml_test.defaultsPrinter panicked: boom")
	})
}
