package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func ExampleLayers() {
	ctx := context.Background()

	base, err := niceyaml.NewSourceFromString(
		"server:\n  host: example.com\n  port: 0\n",
		niceyaml.WithName("base.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	prod, err := niceyaml.NewSourceFromString(
		"server:\n  host: prod.example.com\nservers:\n  - host: cache.internal\n",
		niceyaml.WithName("prod.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	// The port of the server comes from base.yaml, and the list of servers
	// from prod.yaml.
	_, err = niceyaml.NewLayers(base, prod).Decode[layerConfig](ctx)
	fmt.Println(niceyaml.FormatError(err, 0))

	// Output:
	// |-- prod.yaml:4:5: $.servers[0].port: port must be at least 1
	// `-- base.yaml:3:9: $.server.port: port must be at least 1
	//
	// prod.yaml
	//    4 |   - host: cache.internal
	//      |     ^^^^ port must be at least 1
	//
	// base.yaml
	//    3 |   port: 0
	//      |         ^ port must be at least 1
}

func TestLayers_SelfValidate(t *testing.T) {
	t.Parallel()

	const (
		portMessage = "port must be at least 1"

		// A base file whose server names a host and an invalid port.
		badPort = "server:\n  host: example.com\n  port: 0\n"
	)

	// Each case decodes its layers into one value, lowest first, and
	// validates the value through the last one, the top layer. The files
	// are base.yaml and prod.yaml, with mid.yaml between them when the
	// case has three.
	tcs := map[string]struct {
		// Changes the value once every layer has set it, as the environment
		// of a program does.
		change func(cfg *layerConfig)
		// The error of the top layer on its own, which binds every path in
		// that layer.
		without string
		// The error of the layers.
		err string
		// The documents of the layers, lowest first.
		layers []string
		// The paths the value itself reports, as a parent that checks the
		// values below it does.
		odd []paths.Path
	}{
		"a struct field binds in the layer that sets it": {
			layers:  []string{badPort, "server:\n  host: prod.example.com\n"},
			without: "prod.yaml:1:1: $.server.port: " + portMessage,
			err:     "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"the top layer keeps a field it sets": {
			layers:  []string{"server:\n  host: example.com\n  port: 80\n", "server:\n  port: 0\n"},
			without: "prod.yaml:2:9: $.server.port: " + portMessage,
			err:     "prod.yaml:2:9: $.server.port: " + portMessage,
		},
		"a slice element binds in the layer that holds the slice": {
			layers:  []string{"servers:\n  - {host: a, port: 80}\n", "servers:\n  - {host: x}\n"},
			without: "prod.yaml:2:6: $.servers[0].port: " + portMessage,
			err:     "prod.yaml:2:6: $.servers[0].port: " + portMessage,
		},
		"an array element binds in the layer that holds the array": {
			layers:  []string{"pair:\n  - {host: a, port: 80}\n", "pair:\n  - {host: x}\n"},
			without: "prod.yaml:2:6: $.pair[0].port: " + portMessage,
			err:     "prod.yaml:2:6: $.pair[0].port: " + portMessage,
		},
		"a map entry binds in the layer that holds the map": {
			layers:  []string{"backends:\n  a: {host: a, port: 80}\n", "backends:\n  a: {host: x}\n"},
			without: "prod.yaml:2:3: $.backends.a.port: " + portMessage,
			err:     "prod.yaml:2:3: $.backends.a.port: " + portMessage,
		},
		"a value of an interface type binds in the layer that holds it": {
			layers:  []string{"extra: {k: v}\n", "extra: {z: y}\n"},
			odd:     []paths.Path{paths.Current().Child("extra", "k")},
			without: "prod.yaml:1:1: $.extra.k: odd value",
			err:     "prod.yaml:1:1: $.extra.k: odd value",
		},
		"a value that decodes itself binds in the layer that holds it": {
			layers:  []string{"switch: {on: true, mode: fast}\n", "switch: {on: false}\n"},
			odd:     []paths.Path{paths.Current().Child("switch", "mode")},
			without: "prod.yaml:1:1: $.switch.mode: odd value",
			err:     "prod.yaml:1:1: $.switch.mode: odd value",
		},
		"a null field keeps the value of the layer below": {
			layers:  []string{badPort, "server:\n  host: x\n  port: null\n"},
			without: "prod.yaml:3:9: $.server.port: " + portMessage,
			err:     "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a null struct keeps the value of the layer below": {
			layers:  []string{badPort, "server: null\n"},
			without: "prod.yaml:1:1: $.server.port: " + portMessage,
			err:     "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"an empty mapping keeps the value of the layer below": {
			layers:  []string{badPort, "server: {}\n"},
			without: "prod.yaml:1:1: $.server.port: " + portMessage,
			err:     "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a null sets a pointer field, so the error binds at the null": {
			layers:  []string{"limits:\n  timeout: 5\n", "limits:\n  timeout: null\n"},
			without: "prod.yaml:2:12: $.limits.timeout: timeout is required",
			err:     "prod.yaml:2:12: $.limits.timeout: timeout is required",
		},
		"the middle of three layers sets the value": {
			layers: []string{
				"server:\n  host: example.com\n  port: 80\n",
				"server:\n  port: 0\n",
				"server:\n  host: prod.example.com\n",
			},
			without: "prod.yaml:1:1: $.server.port: " + portMessage,
			err:     "mid.yaml:2:9: $.server.port: " + portMessage,
		},
		"the search passes over a null in a middle layer": {
			layers:  []string{badPort, "name: mid\n", "server:\n  port: null\n"},
			without: "prod.yaml:2:9: $.server.port: " + portMessage,
			err:     "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"an inline struct binds in the layer that sets its field": {
			layers:  []string{"name: base\nregion: nowhere\n", "name: prod\n"},
			without: "prod.yaml:1:1: $.region: unknown region",
			err:     "base.yaml:2:9: $.region: unknown region",
		},
		"a pointer to a struct binds in the layer that sets its field": {
			layers:  []string{"backup:\n  host: example.com\n  port: 0\n", "backup:\n  host: x\n"},
			without: "prod.yaml:1:1: $.backup.port: " + portMessage,
			err:     "base.yaml:3:9: $.backup.port: " + portMessage,
		},
		"a struct two fields down binds in the layer that sets its field": {
			layers:  []string{"nested:\n  inner:\n    host: example.com\n    port: 0\n", "name: prod\n"},
			without: "prod.yaml:1:1: $.nested.inner.port: " + portMessage,
			err:     "base.yaml:4:11: $.nested.inner.port: " + portMessage,
		},
		"a slice that only a lower layer holds binds there": {
			layers:  []string{"name: base\nservers:\n  - {host: a, port: 0}\n", "name: prod\n"},
			without: "prod.yaml: $.servers[0].port: " + portMessage,
			err:     "base.yaml:3:21: $.servers[0].port: " + portMessage,
		},
		"a map that only a lower layer holds binds there": {
			layers:  []string{"name: base\nbackends:\n  a: {host: a, port: 0}\n", "name: prod\n"},
			without: "prod.yaml:1:1: $.backends.a.port: " + portMessage,
			err:     "base.yaml:3:22: $.backends.a.port: " + portMessage,
		},
		"each problem of a summary binds in its own layer": {
			layers: []string{
				"server: {host: example.com, port: 1}\nbackends:\n  a: {host: a, port: 80}\n",
				"backends:\n  a: {host: x, port: 2}\n",
			},
			odd: []paths.Path{
				paths.Current().Child("server", "host"),
				paths.Current().Child("backends", "a", "port"),
			},
			without: "prod.yaml: config checks\n" +
				"prod.yaml:1:1: $.server.host: odd value\n" +
				"prod.yaml:2:22: $.backends.a.port: odd value",
			err: "prod.yaml: config checks\n" +
				"base.yaml:1:16: $.server.host: odd value\n" +
				"prod.yaml:2:22: $.backends.a.port: odd value",
		},
		"a value no layer sets binds at the deepest mapping that lacks it": {
			layers:  []string{"name: base\nserver:\n  host: example.com\n", "name: prod\n"},
			without: "prod.yaml:1:1: $.server.port: " + portMessage,
			err:     "base.yaml:2:1: $.server.port: " + portMessage,
		},
		"the top layer wins among mappings of one depth": {
			layers:  []string{"name: base\n", "name: prod\n"},
			odd:     []paths.Path{paths.Current().Child("server", "host")},
			without: "prod.yaml:1:1: $.server.host: odd value",
			err:     "prod.yaml:1:1: $.server.host: odd value",
		},
		"a name no field decodes stays in the top layer": {
			layers:  []string{"unknown: {k: v}\n", "name: prod\n"},
			odd:     []paths.Path{paths.Current().Child("unknown", "k")},
			without: "prod.yaml:1:1: $.unknown.k: odd value",
			err:     "prod.yaml:1:1: $.unknown.k: odd value",
		},
		// The decoder hands the server the value the backup decoded for the
		// anchor, whole, so the port 80 of the base file is gone.
		"a field written as an alias binds in its layer": {
			layers: []string{
				"server:\n  host: example.com\n  port: 80\n",
				"backup: &b {host: x}\nserver: *b\n",
			},
			without: "prod.yaml:1:1: $.backup.port: " + portMessage + "\n" +
				"prod.yaml:2:1: $.server.port: " + portMessage,
			err: "prod.yaml:1:1: $.backup.port: " + portMessage + "\n" +
				"prod.yaml:2:1: $.server.port: " + portMessage,
		},
		// Two limits that Layers documents.
		"a value the caller set binds at what a file holds": {
			layers: []string{"server:\n  host: example.com\n  port: 8080\n", "server:\n  host: x\n"},
			change: func(cfg *layerConfig) {
				cfg.Server.Port = 0
			},
			without: "prod.yaml:1:1: $.server.port: " + portMessage,
			err:     "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a key a lower layer spells unlike its value binds at the key of its map": {
			layers:  []string{"name: base\nrates:\n  1.50: {limit: 0}\n", "name: prod\n"},
			without: "prod.yaml:1:1: $.rates.'1.5'.limit: limit must be at least 1",
			err:     "base.yaml:2:1: $.rates.'1.5'.limit: limit must be at least 1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var cfg layerConfig

			nodes := layerNodes(t, tc.layers...)
			layers := niceyaml.NewLayers(nodes...)

			err := layers.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			cfg.odd = tc.odd

			if tc.change != nil {
				tc.change(&cfg)
			}

			err = nodes[len(nodes)-1].SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.without)

			err = layers.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestLayers_DecodeInto(t *testing.T) {
	t.Parallel()

	const (
		baseInput = "server:\n  host: example.com\n  port: 0\n"
		prodInput = "server:\n  host: prod.example.com\n"

		want = "base.yaml:3:9: $.server.port: port must be at least 1"
	)

	t.Run("a decode validates once and binds in the layer below", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)
		base := nodes[0]

		var cfg layerConfig

		err := niceyaml.NewLayers(nodes...).DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, want)
		assert.True(t, niceyaml.IsInvalid(err))
		assert.Equal(t, "prod.example.com", cfg.Server.Host)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, base, bound.Node())
		assert.Same(t, base, bound.Document())
		assert.Same(t, base.Source(), bound.Source())

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, "$.server.port", path.String())
	})

	t.Run("a layer that fails alone passes once a layer above completes it", func(t *testing.T) {
		t.Parallel()

		// The base names a host and no port, which its own decode rejects.
		nodes := layerNodes(t, "server:\n  host: example.com\n", "server:\n  port: 80\n")

		var alone layerConfig

		err := nodes[0].DecodeInto(t.Context(), &alone)
		require.EqualError(t, err, "base.yaml:1:1: $.server.port: port must be at least 1")

		cfg, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "example.com", Port: 80}, cfg.Server)
	})

	t.Run("the nearest layer below binds", func(t *testing.T) {
		t.Parallel()

		// Both layers below the top one set the port, and the nearer one
		// holds the value the decode kept.
		nodes := layerNodes(t, "server:\n  port: 0\n", baseInput, prodInput)

		_, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context())
		require.EqualError(t, err, "mid.yaml:3:9: $.server.port: port must be at least 1")

		_, err = niceyaml.NewLayers(nodes[1], nodes[0], nodes[2]).Decode[layerConfig](t.Context())
		require.EqualError(t, err, "base.yaml:2:9: $.server.port: port must be at least 1")
	})

	t.Run("a nil Node adds nothing", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)

		_, err := niceyaml.NewLayers(nil, nodes[0], nil, nodes[1], nil).Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)

		_, err = niceyaml.NewLayers(nil, nodes[0]).Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)
	})

	t.Run("self-validation off leaves the value to the caller", func(t *testing.T) {
		t.Parallel()

		layers := niceyaml.NewLayers(layerNodes(t, baseInput, prodInput)...)

		var cfg layerConfig

		err := layers.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		err = layers.SelfValidate(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
		require.EqualError(t, err, want)

		cfg.Server.Port = 8080

		err = layers.SelfValidate(t.Context(), &cfg)
		require.NoError(t, err)
	})

	t.Run("a decode stops at the first layer that fails", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "server:\n  port: many\n", "name: prod\n")

		var cfg layerConfig

		err := niceyaml.NewLayers(nodes...).DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "base.yaml:2:9: $.server.port: expected integer, got string")
		assert.Empty(t, cfg.Name)
	})

	t.Run("a layer that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)

		docs := niceyaml.NewSourceFromString("server: [\n", niceyaml.WithName("mid.yaml")).AllDocuments()
		require.Len(t, docs, 1)

		_, err := niceyaml.NewLayers(nodes[0], docs[0], nodes[1]).Decode[layerConfig](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.ErrorIs(t, err, docs[0].Err())
	})

	t.Run("scoped Nodes bind the path as the highest layer reads it", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t,
			"defaults:\n  server:\n    host: example.com\n    port: 0\n",
			niceyaml.WithName("base.yaml"),
		)
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"))

		baseServer := yamltest.At(t, base, paths.Current().Child("defaults", "server"))
		prodServer := yamltest.At(t, prod, paths.Current().Child("server"))

		_, err := niceyaml.NewLayers(baseServer, prodServer).Decode[layerServer](t.Context())
		require.EqualError(t, err, "base.yaml:4:11: $.server.port: port must be at least 1")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, baseServer, bound.Node())
	})

	t.Run("a validator runs on each layer and sees that layer alone", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, "server:\n  port: 80\n")

		var seen []*niceyaml.Node

		record := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = append(seen, n)

			return nil
		})

		_, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context(), niceyaml.WithValidator(record))
		require.NoError(t, err)
		assert.Equal(t, nodes, seen)
	})

	t.Run("a target that is no pointer returns an error", func(t *testing.T) {
		t.Parallel()

		layers := niceyaml.NewLayers(layerNodes(t, baseInput, prodInput)...)

		err := layers.DecodeInto(t.Context(), layerConfig{})
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)

		err = niceyaml.NewLayers().DecodeInto(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
	})
}

func TestLayers_Empty(t *testing.T) {
	t.Parallel()

	const want = "$.server.port: port must be at least 1"

	var zero niceyaml.Layers

	tcs := map[string]*niceyaml.Layers{
		"no Nodes":       niceyaml.NewLayers(),
		"only nil Nodes": niceyaml.NewLayers(nil, nil),
		"the zero value": &zero,
		"a nil pointer":  nil,
	}

	for name, layers := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The value came from no file, as defaults do.
			cfg := layerConfig{Server: layerServer{Host: "example.com"}}

			err := layers.DecodeInto(t.Context(), &cfg)
			require.EqualError(t, err, want)
			assert.Equal(t, "example.com", cfg.Server.Host)

			err = layers.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, want)

			err = layers.Bind(&cfg, niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("server"))))
			require.EqualError(t, err, "$.server: bad")

			cfg.Server.Port = 80

			got, err := layers.Decode[layerConfig](t.Context())
			require.NoError(t, err)
			assert.Empty(t, got.Server)

			require.NoError(t, layers.SelfValidate(t.Context(), &cfg))
		})
	}
}

func TestLayers_Bind(t *testing.T) {
	t.Parallel()

	nodes := layerNodes(t,
		"server:\n  host: example.com\n  port: 8080\nservers:\n  - {host: a, port: 80}\n",
		"server:\n  host: prod.example.com\nbackends:\n  a: {host: x, port: 80}\n",
	)
	layers := niceyaml.NewLayers(nodes...)

	var cfg layerConfig

	err := layers.DecodeInto(t.Context(), &cfg)
	require.NoError(t, err)

	portPath := paths.Doc().Child("server", "port")

	tcs := map[string]struct {
		v    any
		err  error
		want string
	}{
		"a field binds in the layer that sets it": {
			v:    &cfg,
			err:  niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
			want: "base.yaml:3:9: $.server.port: port is taken",
		},
		"a value that is no pointer names the same type": {
			v:    cfg,
			err:  niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
			want: "base.yaml:3:9: $.server.port: port is taken",
		},
		"a field the top layer sets binds there": {
			v:    &cfg,
			err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			want: "prod.yaml:2:9: $.server.host: unknown host",
		},
		"an element binds in the layer that holds the slice": {
			v:    &cfg,
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("servers").Index(0).Child("port"))),
			want: "base.yaml:5:21: $.servers[0].port: bad",
		},
		"a plain error binds with no location": {
			v:    &cfg,
			err:  errors.New("quota service: connection refused"),
			want: "prod.yaml: quota service: connection refused",
		},
		"a join binds each branch in its layer": {
			v: &cfg,
			err: errors.Join(
				niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
				niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			),
			want: "base.yaml:3:9: $.server.port: port is taken\n" +
				"prod.yaml:2:9: $.server.host: unknown host",
		},
		"a nil value binds every path in the top layer": {
			err:  niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
			want: "prod.yaml:1:1: $.server.port: port is taken",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, layers.Bind(tc.v, tc.err), tc.want)
		})
	}

	t.Run("a nil error stays nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, layers.Bind(&cfg, nil))
	})
}

func TestLayers_Alias(t *testing.T) {
	t.Parallel()

	shared := niceyaml.NewSourceFromString(
		"shared: &shared {host: shared.example.com, port: 0}\n",
		niceyaml.WithName("shared.yaml"),
	)

	// The top layer reads its server from a reference document, which a
	// path cannot follow, and the base file sets a port of its own. Each
	// error stays in the top layer, where the alias may bring the value in.
	tcs := map[string]struct {
		prod string
		err  string
	}{
		"an alias the top layer cannot follow binds at the alias": {
			prod: "server: *shared\n",
			err:  "prod.yaml:1:9: $.server.port: port must be at least 1",
		},
		"a merge key the top layer cannot follow binds in the top layer": {
			prod: "server:\n  <<: *shared\n",
			err:  "prod.yaml: $.server.port: port must be at least 1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			base := yamltest.FirstDocument(
				t,
				"server:\n  host: example.com\n  port: 80\n",
				niceyaml.WithName("base.yaml"),
			)
			prod := yamltest.FirstDocument(t, tc.prod, niceyaml.WithName("prod.yaml"), niceyaml.WithReferences(shared))

			var cfg layerConfig

			err := niceyaml.NewLayers(base, prod).DecodeInto(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)

			err = prod.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			require.ErrorIs(t, bound.Unresolved(), paths.ErrAlias)
		})
	}
}

// layerNodes returns the root Node of each of inputs, in the order they
// apply. The first input is base.yaml and the last is prod.yaml, with
// mid.yaml between them. A single input is base.yaml.
func layerNodes(t *testing.T, inputs ...string) []*niceyaml.Node {
	t.Helper()

	nodes := make([]*niceyaml.Node, len(inputs))

	for i, input := range inputs {
		name := "mid.yaml"

		switch i {
		case 0:
			name = "base.yaml"
		case len(inputs) - 1:
			name = "prod.yaml"
		}

		nodes[i] = yamltest.FirstDocument(t, input, niceyaml.WithName(name))
	}

	return nodes
}

// layerServer is a server that needs a port once it names a host, so
// a server no layer sets passes.
type layerServer struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func (s layerServer) Validate() error {
	if s.Host != "" && s.Port < 1 {
		return niceyaml.NewError("port must be at least 1", niceyaml.AtPath(paths.Current().Child("port")))
	}

	return nil
}

// layerNested holds a [layerServer] one struct down.
type layerNested struct {
	Inner layerServer `yaml:"inner"`
}

// layerLimits needs a timeout, which a null in a document sets to nil.
type layerLimits struct {
	Timeout *int `yaml:"timeout"`
}

func (l layerLimits) Validate() error {
	if l.Timeout == nil {
		return niceyaml.NewError("timeout is required", niceyaml.AtPath(paths.Current().Child("timeout")))
	}

	return nil
}

// layerRate is a rate that needs a limit.
type layerRate struct {
	Limit int `yaml:"limit"`
}

func (r layerRate) Validate() error {
	if r.Limit < 1 {
		return niceyaml.NewError("limit must be at least 1", niceyaml.AtPath(paths.Current().Child("limit")))
	}

	return nil
}

// layerSwitch decodes itself into a new value, so a second decode
// replaces it whole.
type layerSwitch struct {
	Mode string `yaml:"mode"`
	On   bool   `yaml:"on"`
}

func (s *layerSwitch) UnmarshalYAML(unmarshal func(any) error) error {
	type plain layerSwitch

	var decoded plain

	err := unmarshal(&decoded)
	if err != nil {
		return err
	}

	*s = layerSwitch(decoded)

	return nil
}

// LayerCommon holds the field [layerConfig] inlines.
type LayerCommon struct {
	Region string `yaml:"region"`
}

func (c LayerCommon) Validate() error {
	if c.Region == "nowhere" {
		return niceyaml.NewError("unknown region", niceyaml.AtPath(paths.Current().Child("region")))
	}

	return nil
}

// layerConfig holds a field of each kind that a second decode into
// one value merges or replaces. The go-yaml decoder fills the fields in
// the order the struct declares them, and the alias case of
// [TestLayers_SelfValidate] needs Backup to decode before Server.
type layerConfig struct {
	LayerCommon `yaml:",inline"`

	Extra    any                    `yaml:"extra"`
	Backends map[string]layerServer `yaml:"backends"`
	Rates    map[float64]layerRate  `yaml:"rates"`
	Backup   *layerServer           `yaml:"backup"`
	Limits   *layerLimits           `yaml:"limits"`
	Name     string                 `yaml:"name"`
	Servers  []layerServer          `yaml:"servers"`
	Nested   layerNested            `yaml:"nested"`
	Server   layerServer            `yaml:"server"`
	Switch   layerSwitch            `yaml:"switch"`
	Pair     [2]layerServer         `yaml:"pair"`

	// The paths Validate reports, which a test sets after the decodes.
	odd []paths.Path
}

// Validate reports an error at each path in odd under one summary, as a
// parent that checks the values below it does.
func (c layerConfig) Validate() error {
	if len(c.odd) == 0 {
		return nil
	}

	errs := make([]error, 0, len(c.odd))
	for _, path := range c.odd {
		errs = append(errs, niceyaml.NewError("odd value", niceyaml.AtPath(path)))
	}

	return niceyaml.NewSummary("config checks", errs...)
}
