package niceyaml_test

import (
	"context"
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

// defaultsDecoding gives the café and each printer their defaults, and
// sets the port of the kitchen printer and the target of spare.
var defaultsDecoding = niceyaml.DecodeOptions(
	niceyaml.WithDefaults(func(c *defaultsCafe) {
		c.Seats = 20
		c.Currency = "SEK"
		c.Kitchen.Port = 9101
		c.Spare = &defaultsPrinter{Host: "spare", Port: 9200}
	}),
	niceyaml.WithDefaults(func(p *defaultsPrinter) {
		p.Port = 9100
	}),
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

	got := niceyaml.Defaults[defaultsCafe](defaultsDecoding)

	assert.Equal(t, defaultedCafe(), got)

	assert.Equal(t, defaultsPrinter{Port: 9100}, niceyaml.Defaults[defaultsPrinter](defaultsDecoding))
	assert.Equal(t, defaultsCafe{}, niceyaml.Defaults[defaultsCafe]())
	assert.Nil(t, niceyaml.Defaults[*defaultsPrinter](defaultsDecoding))

	// A later WithDefaults for a type replaces an earlier one.
	later := niceyaml.Defaults[defaultsPrinter](defaultsDecoding,
		niceyaml.WithDefaults(func(p *defaultsPrinter) { p.Host = "lp" }))
	assert.Equal(t, defaultsPrinter{Host: "lp"}, later)

	// Each call builds new values, so a map the function sets belongs to
	// one value.
	labels := niceyaml.WithDefaults(func(c *defaultsCafe) { c.Labels = map[string]string{"floor": "1"} })
	first := niceyaml.Defaults[defaultsCafe](labels)
	first.Labels["floor"] = "2"
	assert.Equal(t, "1", niceyaml.Defaults[defaultsCafe](labels).Labels["floor"])

	assert.PanicsWithValue(t, "boom", func() {
		niceyaml.Defaults[defaultsPrinter](niceyaml.WithDefaults(func(*defaultsPrinter) { panic("boom") }))
	})
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
	start := niceyaml.Defaults[defaultsCafe](defaultsDecoding)
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

	set := niceyaml.WithDefaults(func(p *pair) { p.C = &defaultsPrinter{Port: 1} })

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
		}](t.Context(), niceyaml.WithDefaults(func(i *int) { *i = 7 }))
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
		niceyaml.WithDefaults(func(c *Common) {
			c.Name = "inner"
			c.Tier = 2
		}),
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
		niceyaml.WithDefaults(func(l *defaultsLevel) { l.Level = 3 }))
	require.NoError(t, err)
	assert.Equal(t, []defaultsLevel{{Name: "info", Level: 3}, {Name: "debug", Level: 3}}, got)
}

func TestNode_Decode_DefaultsPanic(t *testing.T) {
	t.Parallel()

	boom := niceyaml.WithDefaults(func(*defaultsPrinter) { panic("boom") })

	t.Run("a value the decode creates", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "stations: [{host: a}]\n")

		_, err := doc.Decode[defaultsCafe](t.Context(), niceyaml.WithDefaults(func(*defaultsCafe) {}), boom)
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
