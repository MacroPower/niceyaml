package niceyaml_test

import (
	"context"
	"reflect"
	"testing"

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

	// The places that start from equal values share the target, as the
	// decoder shares it, and the one that starts from another value gets
	// its own.
	assert.Equal(t, &defaultsPrinter{Host: "h", Port: 9100}, got.A)
	assert.Same(t, got.A, got.B)
	assert.Equal(t, &defaultsPrinter{Host: "h", Port: 1}, got.C)

	// An interface value holds the value of its anchor.
	assert.Equal(t, map[string]any{"k": "v"}, got.Y)
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

	// The places that start from equal values share the target.
	assert.Same(t, got.Backups[0], got.Backups[1])

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

	// The decode cannot read the anchor, so each value that the decoder
	// creates inside it keeps the value of the decoder, in a decode of the
	// document and in a decode of the Node of the alias.
	doc := yamltest.FirstDocument(t, "stations: *crew\n",
		yamltest.References("crew: &crew [{host: a}]\n"))

	got, err := doc.Decode[defaultsCafe](t.Context(), defaultsDecoding)
	require.NoError(t, err)
	assert.Equal(t, []defaultsPrinter{{Host: "a"}}, got.Stations)

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
		// The decoder starts each element from zero, so the element decodes
		// again from its defaults.
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
		// the fill cannot read inside them.
		"entry before a merge key that replaces it": {
			input: stringtest.LinesLF("stations: [{host: s}]", "<<: *crew"),
			want: defaultedCafe(func(c *defaultsCafe) {
				c.Stations = []defaultsPrinter{{Host: "c"}}
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
// the int that Calls points to.
type defaultsCounted struct {
	Calls *int
	Name  string
}

// UnmarshalYAML counts the call and decodes a name.
func (c *defaultsCounted) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	*c.Calls++

	return decode(&c.Name)
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
}
