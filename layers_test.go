package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
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
	// base.yaml
	//    3 |   port: 0
	//      |         ^ port must be at least 1
	//
	// prod.yaml
	//    4 |   - host: cache.internal
	//      |     ^^^^ port must be at least 1
}

func ExampleLayers_Document() {
	ctx := context.Background()

	base, err := niceyaml.NewSourceFromString(
		"kind: Service\n\nserver:\n  host: example.com\n  port: 0\n",
		niceyaml.WithName("base.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	prod, err := niceyaml.NewSourceFromString(
		"server:\n  host: prod.example.com\n",
		niceyaml.WithName("prod.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	doc, err := niceyaml.NewLayers(base, prod).Document()
	if err != nil {
		log.Fatal(err)
	}

	// One value of the layers decodes on its own.
	host, err := doc.DecodeAt[string](ctx, paths.Doc().Child("server", "host"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(host)

	// The view holds the text the layers merge into, which is no file.
	fmt.Println(doc.View().Held().Content())

	// An error binds in the file that holds its value.
	fmt.Println(doc.NewError("port must be at least 1", niceyaml.AtPath(paths.Doc().Child("server", "port"))))

	// Output:
	// prod.example.com
	// kind: Service
	// server:
	//   host: prod.example.com
	//   port: 0
	// base.yaml:5:9: $.server.port: port must be at least 1
}

func TestLayers_SelfValidate(t *testing.T) {
	t.Parallel()

	const (
		portMessage = "port must be at least 1"

		// A base file whose server names a host and an invalid port.
		badPort = "server:\n  host: example.com\n  port: 0\n"
	)

	// Each case decodes its layers into one value and validates the
	// value through them. The files are base.yaml and prod.yaml, with
	// mid.yaml between them when the case has three.
	tcs := map[string]struct {
		// Changes the value once the layers have set it, as the
		// environment of a program does.
		change func(cfg *layerConfig)
		// The error of the layers.
		err string
		// The documents of the layers, lowest first.
		layers []string
		// The paths the value itself reports, as a parent that checks the
		// values below it does.
		odd []paths.Path
	}{
		"a field binds in the layer that sets it": {
			layers: []string{badPort, "server:\n  host: prod.example.com\n"},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"the top layer keeps a field it sets": {
			layers: []string{"server:\n  host: example.com\n  port: 80\n", "server:\n  port: 0\n"},
			err:    "prod.yaml:2:9: $.server.port: " + portMessage,
		},
		"an element binds in the layer that holds the sequence": {
			layers: []string{"servers:\n  - {host: a, port: 80}\n", "servers:\n  - {host: x}\n"},
			err:    "prod.yaml:2:6: $.servers[0].port: " + portMessage,
		},
		"an element of an array binds in the layer that holds the sequence": {
			layers: []string{"pair:\n  - {host: a, port: 80}\n", "pair:\n  - {host: x}\n"},
			err:    "prod.yaml:2:6: $.pair[0].port: " + portMessage,
		},
		"a field of a map entry binds in the layer that sets it": {
			layers: []string{
				"backends:\n  a: {host: a, port: 0}\n",
				"backends:\n  a: {host: x}\n  b: {host: b, port: 1}\n",
			},
			err: "base.yaml:2:22: $.backends.a.port: " + portMessage,
		},
		"a key of an untyped mapping binds in the layer that sets it": {
			layers: []string{"extra: {k: v}\n", "extra: {z: y}\n"},
			odd:    []paths.Path{paths.Current().Child("extra", "k")},
			err:    "base.yaml:1:12: $.extra.k: odd value",
		},
		"a key of a value that decodes itself binds in the layer that sets it": {
			layers: []string{"switch: {on: true, mode: fast}\n", "switch: {on: false}\n"},
			odd:    []paths.Path{paths.Current().Child("switch", "mode")},
			err:    "base.yaml:1:26: $.switch.mode: odd value",
		},
		"a null field keeps the value of the layer below": {
			layers: []string{badPort, "server:\n  host: x\n  port: null\n"},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a null mapping keeps the value of the layer below": {
			layers: []string{badPort, "server: null\n"},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"an empty mapping keeps the value of the layer below": {
			layers: []string{badPort, "server: {}\n"},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a null that no layer sets a value under binds in the highest layer": {
			layers: []string{"limits:\n  timeout: null\n", "limits:\n  timeout: ~\n"},
			err:    "prod.yaml:2:12: $.limits.timeout: timeout is required",
		},
		"the middle of three layers sets the value": {
			layers: []string{
				"server:\n  host: example.com\n  port: 80\n",
				"server:\n  port: 0\n",
				"server:\n  host: prod.example.com\n",
			},
			err: "mid.yaml:2:9: $.server.port: " + portMessage,
		},
		"the search passes over a null in a middle layer": {
			layers: []string{badPort, "name: mid\n", "server:\n  port: null\n"},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"an inline struct binds in the layer that sets its field": {
			layers: []string{"name: base\nregion: nowhere\n", "name: prod\n"},
			err:    "base.yaml:2:9: $.region: unknown region",
		},
		"a pointer to a struct binds in the layer that sets its field": {
			layers: []string{"backup:\n  host: example.com\n  port: 0\n", "backup:\n  host: x\n"},
			err:    "base.yaml:3:9: $.backup.port: " + portMessage,
		},
		"a struct two fields down binds in the layer that sets its field": {
			layers: []string{"nested:\n  inner:\n    host: example.com\n    port: 0\n", "name: prod\n"},
			err:    "base.yaml:4:11: $.nested.inner.port: " + portMessage,
		},
		"a sequence that only a lower layer holds binds there": {
			layers: []string{"name: base\nservers:\n  - {host: a, port: 0}\n", "name: prod\n"},
			err:    "base.yaml:3:21: $.servers[0].port: " + portMessage,
		},
		"a mapping that only a lower layer holds binds there": {
			layers: []string{"name: base\nbackends:\n  a: {host: a, port: 0}\n", "name: prod\n"},
			err:    "base.yaml:3:22: $.backends.a.port: " + portMessage,
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
			err: "base.yaml: config checks\n" +
				"base.yaml:1:16: $.server.host: odd value\n" +
				"prod.yaml:2:22: $.backends.a.port: odd value",
		},
		"a value no layer sets binds at the deepest mapping that lacks it": {
			layers: []string{"name: base\nserver:\n  host: example.com\n", "name: prod\n"},
			err:    "base.yaml:2:1: $.server.port: " + portMessage,
		},
		"the top layer wins among mappings of one depth": {
			layers: []string{"name: base\n", "name: prod\n"},
			odd:    []paths.Path{paths.Current().Child("server", "host")},
			err:    "prod.yaml:1:1: $.server.host: odd value",
		},
		"a key no field reads binds in the layer that sets it": {
			layers: []string{"unknown: {k: v}\n", "name: prod\n"},
			odd:    []paths.Path{paths.Current().Child("unknown", "k")},
			err:    "base.yaml:1:14: $.unknown.k: odd value",
		},
		// The alias brings in a host and no port, so the port of the base
		// file stays, and only the backup lacks one.
		"a value an alias brings in merges as one written in its place": {
			layers: []string{
				"server:\n  host: example.com\n  port: 80\n",
				"backup: &b {host: x}\nserver: *b\n",
			},
			err: "prod.yaml:1:1: $.backup.port: " + portMessage,
		},
		"a value under an alias binds at the content of the anchor": {
			layers: []string{
				"server:\n  host: example.com\n  port: 80\n",
				"extra: &s {host: x, port: 0}\nserver: *s\n",
			},
			err: "prod.yaml:1:27: $.server.port: " + portMessage,
		},
		"a key spelled unlike its Go value binds at its entry": {
			layers: []string{"name: base\nrates:\n  1.50: {limit: 0}\n", "name: prod\n"},
			err:    "base.yaml:3:17: $.rates.'1.50'.limit: limit must be at least 1",
		},
		// The limit that Layers documents.
		"a value the caller set binds at what a file holds": {
			layers: []string{"server:\n  host: example.com\n  port: 8080\n", "server:\n  host: x\n"},
			change: func(cfg *layerConfig) {
				cfg.Server.Port = 0
			},
			err: "base.yaml:3:9: $.server.port: " + portMessage,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var cfg layerConfig

			layers := niceyaml.NewLayers(layerNodes(t, tc.layers...)...)

			err := layers.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			cfg.odd = tc.odd

			if tc.change != nil {
				tc.change(&cfg)
			}

			err = layers.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)
		})
	}

	t.Run("a layer that did not parse holds no value", func(t *testing.T) {
		t.Parallel()

		const want = "base.yaml:3:9: $.server.port: " + portMessage

		nodes := layerNodes(t, badPort, "server:\n  host: prod.example.com\n")
		base, prod := nodes[0], nodes[1]

		var cfg layerConfig

		err := niceyaml.NewLayers(base, prod).DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		docs := niceyaml.NewSourceFromString("server: [\n", niceyaml.WithName("mid.yaml")).AllDocuments()
		require.Len(t, docs, 1)
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)

		// The error binds in the layer below the one that did not parse,
		// whether that one lies between two layers or on top.
		err = niceyaml.NewLayers(base, docs[0], prod).SelfValidate(t.Context(), &cfg)
		require.EqualError(t, err, want)

		err = niceyaml.NewLayers(base, docs[0]).SelfValidate(t.Context(), &cfg)
		require.EqualError(t, err, want)

		err = niceyaml.NewLayers(base, docs[0]).Bind(
			niceyaml.NewError(portMessage, niceyaml.AtPath(paths.Doc().Child("server", "port"))),
		)
		require.EqualError(t, err, want)

		// On its own it resolves no path, so the error names the file
		// and no position.
		err = niceyaml.NewLayers(docs[0]).SelfValidate(t.Context(), &cfg)
		require.EqualError(t, err, "mid.yaml: $.server.port: "+portMessage)
	})
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
		// holds the value the merge kept.
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

	t.Run("a null keeps the value below it in a pointer field too", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "limits:\n  timeout: 5\n", "limits:\n  timeout: null\n")

		cfg, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context())
		require.NoError(t, err)
		require.NotNil(t, cfg.Limits)
		require.NotNil(t, cfg.Limits.Timeout)
		assert.Equal(t, 5, *cfg.Limits.Timeout)
	})

	t.Run("one decode reports the values every layer gets wrong", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "server:\n  port: many\n", "name: [prod]\n")

		var cfg layerConfig

		err := niceyaml.NewLayers(nodes...).DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "base.yaml: 2 problems\n"+
			"base.yaml:2:9: $.server.port: expected integer, got string\n"+
			"prod.yaml:1:8: $.name: expected string, got sequence")
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("a key no field reads reports in the layer that holds it", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "server:\n  prot: 80\n", "server:\n  host: x\n  port: 1\nzone: prod\n")

		_, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](
			t.Context(), niceyaml.WithDisallowUnknownFields(true),
		)
		require.EqualError(t, err, "base.yaml: 2 unknown fields\n"+
			"base.yaml:2:3: $.server.prot~: unknown field \"prot\"\n"+
			"prod.yaml:4:1: $.zone~: unknown field \"zone\"")
	})

	t.Run("a value that holds defaults follows the rule of a decode", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "backends:\n  a: {host: a, port: 1}\n", "backends:\n  b: {host: b, port: 2}\n")

		// A field the layers leave out keeps its default. The mapping of
		// the merged document replaces the map the value holds, so the
		// default entry is gone, where the entry of the base file stays.
		cfg := layerConfig{
			Name:     "default",
			Backends: map[string]layerServer{"d": {Host: "d", Port: 9}},
			Servers:  []layerServer{{Host: "s", Port: 9}},
		}

		err := niceyaml.NewLayers(nodes...).DecodeInto(t.Context(), &cfg)
		require.NoError(t, err)
		assert.Equal(t, "default", cfg.Name)
		assert.Equal(t, []layerServer{{Host: "s", Port: 9}}, cfg.Servers)
		assert.Equal(t, map[string]layerServer{
			"a": {Host: "a", Port: 1},
			"b": {Host: "b", Port: 2},
		}, cfg.Backends)
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

	t.Run("the lowest layer that fails returns its error", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "server: *missing\n", baseInput, "name: *gone\n")

		_, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context())
		require.EqualError(t, err, "base.yaml:1:9: $.server: could not find alias \"missing\"")

		// The layer fails as a decode of it alone does.
		_, alone := nodes[0].Decode[any](t.Context())
		require.EqualError(t, alone, err.Error())
	})

	t.Run("scoped Nodes bind in their files with the paths of those files", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t,
			"defaults:\n  server:\n    host: example.com\n    port: 0\n",
			niceyaml.WithName("base.yaml"),
		)
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"))

		baseServer := yamltest.At(t, base, paths.Current().Child("defaults", "server"))
		prodServer := yamltest.At(t, prod, paths.Current().Child("server"))

		layers := niceyaml.NewLayers(baseServer, prodServer)

		server, err := layers.Decode[layerServer](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "prod.example.com"}, server)

		// The merged document holds the port at $.port, and the base
		// file holds it under its own path.
		_, err = layers.Decode[layerServer](t.Context())
		require.EqualError(t, err, "base.yaml:4:11: $.defaults.server.port: port must be at least 1")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, baseServer, bound.Node())
		assert.Same(t, base, bound.Document())

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, "$.defaults.server.port", path.String())

		var located *niceyaml.Error

		require.ErrorAs(t, err, &located)

		path, ok = located.Path()
		require.True(t, ok)
		assert.Equal(t, "$.defaults.server.port", path.String())

		// A path reads from the merged value whether it starts at `$` or
		// at `@`, and each error reports the path of its own file.
		scoped := map[string]struct {
			err  error
			want string
		}{
			"a path from the root": {
				err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("host"))),
				want: "prod.yaml:2:9: $.server.host: unknown host",
			},
			"a path from the value": {
				err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Current().Child("host"))),
				want: "prod.yaml:2:9: $.server.host: unknown host",
			},
			"a key": {
				err:  niceyaml.NewError("bad key", niceyaml.AtPath(paths.Doc().Child("port").Key())),
				want: "base.yaml:4:5: $.defaults.server.port~: bad key",
			},
			"the value itself": {
				err:  niceyaml.NewError("bad server", niceyaml.AtPath(paths.Doc())),
				want: "prod.yaml:2:3: $.server: bad server",
			},
			"a key no layer holds": {
				err:  niceyaml.NewError("tls is required", niceyaml.AtPath(paths.Doc().Child("tls"))),
				want: "prod.yaml:1:1: $.server.tls: tls is required",
			},
			"a wrapped error": {
				err: fmt.Errorf("check: %w",
					niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Current().Child("host")))),
				want: "prod.yaml:2:9: $.server.host: check: unknown host",
			},
			"a rebased error": {
				err: niceyaml.Rebase(
					niceyaml.NewError("bad port", niceyaml.AtPath(paths.Current())), paths.Current().Child("port"),
				),
				want: "base.yaml:4:11: $.defaults.server.port: bad port",
			},
			"an error with no location": {
				err:  errors.New("quota service: connection refused"),
				want: "base.yaml: quota service: connection refused",
			},
		}

		for name, tc := range scoped {
			require.EqualError(t, layers.Bind(tc.err), tc.want, name)
		}
	})

	t.Run("a validator runs once, on the merged document", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, "server:\n  port: 80\n")

		var seen []*niceyaml.Node

		record := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = append(seen, n)

			return nil
		})

		_, err := niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context(), niceyaml.WithValidator(record))
		require.NoError(t, err)
		require.Len(t, seen, 1)
		assert.NotContains(t, nodes, seen[0])
		assert.Equal(t, "server:\n  host: example.com\n  port: 80", seen[0].View().Held().Content())
	})

	t.Run("a context that ended stops the decode", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		layers := niceyaml.NewLayers(layerNodes(t, baseInput, prodInput)...)

		_, err := layers.Decode[layerConfig](ctx)
		require.ErrorIs(t, err, context.Canceled)

		// The layers merged all the same, so a later call decodes them.
		_, err = layers.Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)
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

func TestLayers_Validate(t *testing.T) {
	t.Parallel()

	const (
		baseInput = "# yaml-language-server: $schema=app.json\nname: shop\nserver:\n  host: example.com\n  port: 0\n"
		prodInput = "server:\n  host: prod.example.com\n"
	)

	requires := schema.MustCompile([]byte(`{
		"type": "object",
		"required": ["name", "server"],
		"properties": {
			"server": {
				"type": "object",
				"required": ["host", "port", "tls"],
				"properties": {"port": {"type": "integer", "minimum": 1}}
			}
		}
	}`))

	t.Run("a schema checks what the layers hold together", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)

		// The name is required, and prod.yaml leaves it to base.yaml.
		err := nodes[1].Validate(t.Context(), requires)
		require.ErrorContains(t, err, `prod.yaml:1:1: $.name: missing required property "name"`)

		// Each violation binds in the layer that holds its value, and the
		// key no layer holds in the highest layer that holds its mapping.
		err = niceyaml.NewLayers(nodes...).Validate(t.Context(), requires)
		require.EqualError(t, err, "base.yaml: 2 schema violations\n"+
			"base.yaml:5:9: $.server.port: 0 is less than 1\n"+
			"prod.yaml:1:1: $.server.tls: missing required property \"tls\"")
		assert.True(t, niceyaml.IsInvalid(err))

		_, err = niceyaml.NewLayers(nodes...).Decode[layerConfig](t.Context(), niceyaml.WithValidator(requires))
		require.ErrorContains(t, err, "base.yaml:5:9: $.server.port: 0 is less than 1")
	})

	t.Run("the merged document takes the name and the preamble of the lowest layer", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{}
		base := yamltest.FirstDocument(t, baseInput,
			niceyaml.WithName("base"), niceyaml.WithFilePath("conf/base.yaml"), niceyaml.WithFS(fsys))
		prod := yamltest.FirstDocument(t, "# yaml-language-server: $schema=other.json\n"+prodInput,
			niceyaml.WithFilePath("conf/prod.yaml"))

		var seen *niceyaml.Node

		err := niceyaml.NewLayers(base, prod).Validate(t.Context(), niceyaml.ValidatorFunc(
			func(_ context.Context, n *niceyaml.Node) error {
				seen = n

				return nil
			},
		))
		require.NoError(t, err)
		require.NotNil(t, seen)

		assert.Equal(t, "base", seen.Source().Name())
		assert.Equal(t, "conf/base.yaml", seen.FilePath())
		assert.Equal(t, fsys, seen.FS())
		assert.True(t, seen.Path().IsRoot())
		assert.Equal(t, 0, seen.DocumentIndex())

		directive := schema.ParseDocumentDirective(seen.Preamble())
		require.NotNil(t, directive)
		assert.Equal(t, "app.json", directive.Schema)

		assert.Equal(t,
			"# yaml-language-server: $schema=app.json\nname: shop\nserver:\n  host: prod.example.com\n  port: 0",
			seen.Source().View().Held().Content())
	})

	t.Run("an error with a position in the merged text binds at the value there", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)

		// The validator reads the positions of the merged text, which is
		// no file, and each binds in the file that holds the value.
		byPosition := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			port, err := n.Ranges(paths.Doc().Child("server", "port"))
			require.NoError(t, err)

			host, err := n.Ranges(paths.Doc().Child("server", "host").Key())
			require.NoError(t, err)

			return errors.Join(
				niceyaml.NewError("at a position", niceyaml.AtPosition(port[0].Start)),
				niceyaml.NewError("at a range", niceyaml.AtRange(port[0])),
				niceyaml.NewError("at a key", niceyaml.AtPosition(host[0].Start)),
				niceyaml.NewError("beside a path",
					niceyaml.AtPath(paths.Doc().Child("server")), niceyaml.AtPosition(port[0].Start)),
				niceyaml.NewError("in the preamble",
					niceyaml.AtPath(paths.Doc().Child("name")), niceyaml.AtPosition(position.New(0, 2))),
				niceyaml.NewError("in the preamble alone", niceyaml.AtPosition(position.New(0, 2))),
				n.NewError("through the Node", niceyaml.AtPath(paths.Current().Child("server", "host"))),
			)
		})

		err := niceyaml.NewLayers(nodes...).Validate(t.Context(), byPosition)
		require.EqualError(t, err, "base.yaml:2:7: $.name: in the preamble\n"+
			"base.yaml:5:9: at a position\n"+
			"base.yaml:5:9: at a range\n"+
			"base.yaml:5:9: $.server: beside a path\n"+
			"prod.yaml:2:3: at a key\n"+
			"prod.yaml:2:9: $.server.host: through the Node\n"+
			"base.yaml: in the preamble alone")
	})

	t.Run("a layer that did not parse returns its error and nothing runs", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("server: [\n", niceyaml.WithName("prod.yaml")).AllDocuments()
		require.Len(t, docs, 1)

		ran := false

		err := niceyaml.NewLayers(layerNodes(t, baseInput)[0], docs[0]).Validate(t.Context(),
			niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
				ran = true

				return nil
			}))
		require.ErrorIs(t, err, docs[0].Err())
		assert.False(t, ran)
	})

	t.Run("a nil validator runs nothing", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, niceyaml.NewLayers(layerNodes(t, baseInput, prodInput)...).Validate(t.Context(), nil))
	})
}

func TestLayers_Document(t *testing.T) {
	t.Parallel()

	// The blank line keeps the lines of base.yaml apart from the lines of
	// the merged document.
	const (
		baseInput = "# base\nname: shop\n\nserver:\n  host: example.com\n  port: 0\n"
		prodInput = "server:\n  host: prod.example.com\n"
	)

	var (
		host = paths.Doc().Child("server", "host")
		port = paths.Doc().Child("server", "port")
	)

	t.Run("the Node is the root of the merged document", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)
		layers := niceyaml.NewLayers(nodes...)

		doc, err := layers.Document()
		require.NoError(t, err)

		assert.NotContains(t, nodes, doc)
		assert.True(t, doc.Path().IsRoot())
		assert.Same(t, doc, doc.Document())
		assert.Equal(t, "base.yaml", doc.Source().Name())
		assert.Equal(t, niceyaml.NodeMapping, doc.Kind())
		assert.Equal(t, "# base\nname: shop\nserver:\n  host: prod.example.com\n  port: 0",
			doc.View().Held().Content())

		// Every call returns the Node a validator gets.
		again, err := layers.Document()
		require.NoError(t, err)
		assert.Same(t, doc, again)

		ran := false

		err = layers.Validate(t.Context(), niceyaml.ValidatorFunc(
			func(_ context.Context, n *niceyaml.Node) error {
				ran = true

				assert.Same(t, doc, n)

				return nil
			},
		))
		require.NoError(t, err)
		assert.True(t, ran)
	})

	t.Run("a caller reads one value of the layers", func(t *testing.T) {
		t.Parallel()

		doc, err := niceyaml.NewLayers(layerNodes(t, baseInput, prodInput)...).Document()
		require.NoError(t, err)

		got, err := doc.DecodeAt[string](t.Context(), host)
		require.NoError(t, err)
		assert.Equal(t, "prod.example.com", got)

		server, err := yamltest.At(t, doc, paths.Doc().Child("server")).Decode[layerServer](
			t.Context(), niceyaml.WithSelfValidation(false),
		)
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "prod.example.com"}, server)

		var tls string

		found, err := doc.DecodeIfPresent(t.Context(), paths.Doc().Child("server", "tls"), &tls)
		require.NoError(t, err)
		assert.False(t, found)

		fields, err := doc.Nodes(paths.Doc().Child("server").ChildAll())
		require.NoError(t, err)
		require.Len(t, fields, 2)
		assert.Equal(t, "$.server.host", fields[0].Path().String())
		assert.Equal(t, "$.server.port", fields[1].Path().String())
	})

	t.Run("an error binds in the file of a layer", func(t *testing.T) {
		t.Parallel()

		doc, err := niceyaml.NewLayers(layerNodes(t, baseInput, prodInput)...).Document()
		require.NoError(t, err)

		server := yamltest.At(t, doc, paths.Doc().Child("server"))

		_, wrongType := doc.DecodeAt[int](t.Context(), host)
		_, missing := doc.At(paths.Doc().Child("server", "tls"))

		tcs := map[string]struct {
			err  error
			want string
		}{
			"at a path": {
				err:  doc.NewError("here", niceyaml.AtPath(port)),
				want: "base.yaml:6:9: $.server.port: here",
			},
			"at a path from a scoped Node": {
				err:  server.NewError("here", niceyaml.AtPath(paths.Current().Child("host"))),
				want: "prod.yaml:2:9: $.server.host: here",
			},
			"at a scoped Node": {
				err:  server.NewError("here"),
				want: "prod.yaml:2:3: $.server: here",
			},
			"of a decode of one value": {
				err:  wrongType,
				want: "prod.yaml:2:9: $.server.host: expected integer, got string",
			},
			"of a path no layer holds": {
				err:  missing,
				want: "prod.yaml:1:1: $.server.tls: not found",
			},
			"with no location": {
				err:  doc.Bind(errors.New("quota service: connection refused")),
				want: "base.yaml: quota service: connection refused",
			},
		}

		for name, tc := range tcs {
			require.EqualError(t, tc.err, tc.want, name)
		}
	})

	t.Run("the positions of the Node read the merged text", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, prodInput)

		doc, err := niceyaml.NewLayers(nodes...).Document()
		require.NoError(t, err)

		merged, err := doc.Ranges(port)
		require.NoError(t, err)

		inBase, err := nodes[0].Ranges(port)
		require.NoError(t, err)

		assert.Equal(t, position.New(4, 8), merged[0].Start)
		assert.Equal(t, position.New(5, 8), inBase[0].Start)

		// The error binds in base.yaml, at the range that file has for
		// the value.
		err = doc.NewError("here", niceyaml.AtPath(port))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, nodes[0], bound.Node())
		assert.Same(t, nodes[0].Source(), bound.Source())

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, inBase[0], rng)

		assert.False(t, niceyaml.Annotate(err, doc.View()))
		assert.True(t, niceyaml.Annotate(err, nodes[0].View()))

		// A position reads the merged text, so the one base.yaml has for
		// the port names no value of the merged document.
		err = doc.NewError("here", niceyaml.AtPosition(merged[0].Start))
		require.EqualError(t, err, "base.yaml:6:9: here")

		err = doc.NewError("here", niceyaml.AtPosition(inBase[0].Start))
		require.EqualError(t, err, "base.yaml: here")
	})

	t.Run("a layer that holds no value returns its error and no Node", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("server: [\n", niceyaml.WithName("prod.yaml")).AllDocuments()
		require.Len(t, docs, 1)

		layers := niceyaml.NewLayers(layerNodes(t, baseInput)[0], docs[0])

		doc, err := layers.Document()
		require.ErrorIs(t, err, docs[0].Err())
		assert.Nil(t, doc)

		// A bind goes on without the layer.
		err = layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(port)))
		require.EqualError(t, err, "base.yaml:6:9: $.server.port: here")
	})

	t.Run("layers that hold no Node return an empty document", func(t *testing.T) {
		t.Parallel()

		var zero niceyaml.Layers

		tcs := map[string]*niceyaml.Layers{
			"no Nodes":       niceyaml.NewLayers(),
			"only nil Nodes": niceyaml.NewLayers(nil, nil),
			"the zero value": &zero,
			"a nil pointer":  nil,
		}

		for name, layers := range tcs {
			doc, err := layers.Document()
			require.NoError(t, err, name)
			require.NotNil(t, doc, name)

			assert.True(t, doc.IsEmpty(), name)
			assert.Empty(t, doc.Source().Name(), name)

			err = doc.NewError("port is required", niceyaml.AtPath(port))
			require.EqualError(t, err, "$.server.port: port is required", name)
		}
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

			err = layers.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("server"))))
			require.EqualError(t, err, "$.server: bad")

			ran := false

			err = layers.Validate(t.Context(), niceyaml.ValidatorFunc(
				func(_ context.Context, n *niceyaml.Node) error {
					ran = true

					assert.Empty(t, n.Source().Name())
					assert.True(t, n.IsEmpty())

					return nil
				},
			))
			require.NoError(t, err)
			assert.True(t, ran)

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

	portPath := paths.Doc().Child("server", "port")

	tcs := map[string]struct {
		err  error
		want string
	}{
		"a field binds in the layer that sets it": {
			err:  niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
			want: "base.yaml:3:9: $.server.port: port is taken",
		},
		"a field the top layer sets binds there": {
			err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			want: "prod.yaml:2:9: $.server.host: unknown host",
		},
		"a path from the value reads as one from the root": {
			err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Current().Child("server", "host"))),
			want: "prod.yaml:2:9: $.server.host: unknown host",
		},
		"a mapping that both layers hold binds in the higher one": {
			err:  niceyaml.NewError("bad server", niceyaml.AtPath(paths.Doc().Child("server"))),
			want: "prod.yaml:2:3: $.server: bad server",
		},
		"a key binds in the layer of its value": {
			err:  niceyaml.NewError("bad key", niceyaml.AtPath(portPath.Key())),
			want: "base.yaml:3:3: $.server.port~: bad key",
		},
		"an element binds in the layer that holds the sequence": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("servers").Index(0).Child("port"))),
			want: "base.yaml:5:21: $.servers[0].port: bad",
		},
		"an element the sequence lacks binds in its layer with no position": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("servers").Index(3).Child("port"))),
			want: "base.yaml: $.servers[3].port: bad",
		},
		"a path below a scalar binds in the layer of the scalar": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(portPath.Child("unit"))),
			want: "base.yaml: $.server.port.unit: bad",
		},
		"an entry binds in the layer that holds it": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("backends", "a", "port"))),
			want: "prod.yaml:4:22: $.backends.a.port: bad",
		},
		"the root binds in the highest layer": {
			err:  niceyaml.NewError("bad config", niceyaml.AtPath(paths.Doc())),
			want: "prod.yaml:1:1: $: bad config",
		},
		"a plain error binds in the lowest layer with no position": {
			err:  errors.New("quota service: connection refused"),
			want: "base.yaml: quota service: connection refused",
		},
		"a join binds each branch in its layer": {
			err: errors.Join(
				niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
				niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			),
			want: "base.yaml:3:9: $.server.port: port is taken\n" +
				"prod.yaml:2:9: $.server.host: unknown host",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, layers.Bind(tc.err), tc.want)
		})
	}

	t.Run("a nil error stays nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, layers.Bind(nil))
	})

	t.Run("no error binds in the merged document", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		// An error with no location belongs to the lowest layer.
		err := layers.Bind(errors.New("quota service: connection refused"))
		require.ErrorAs(t, err, &bound)
		assert.Same(t, nodes[0], bound.Node())
		assert.Same(t, nodes[0].Source(), bound.Source())

		// A located one belongs to the layer that holds its value.
		err = layers.Bind(niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))))
		require.ErrorAs(t, err, &bound)
		assert.Same(t, nodes[1], bound.Node())
		assert.Same(t, nodes[1].Source(), bound.Source())
	})
}

func TestLayers_Alias(t *testing.T) {
	t.Parallel()

	const baseInput = "server:\n  host: example.com\n  port: 0\n"

	// The top layer reads its server from a reference document, which a
	// path cannot follow. The merge reads it, so each error binds in the
	// layer that holds its value.
	tcs := map[string]struct {
		shared string
		prod   string
		err    string
		// Whether the path of the error enters the alias.
		unresolved bool
	}{
		"a value the reference document sets binds at the alias": {
			shared:     "shared: &shared {host: shared.example.com, port: -1}\n",
			prod:       "server: *shared\n",
			err:        "prod.yaml:1:9: $.server.port: port must be at least 1",
			unresolved: true,
		},
		"a value a merge key reads from the reference document binds in its layer": {
			shared: "shared: &shared {host: shared.example.com, port: -1}\n",
			prod:   "server:\n  <<: *shared\n",
			err:    "prod.yaml: $.server.port: port must be at least 1",
		},
		"a value the reference document leaves out binds in the layer below": {
			shared: "shared: &shared {host: shared.example.com}\n",
			prod:   "server: *shared\n",
			err:    "base.yaml:3:9: $.server.port: port must be at least 1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			shared := niceyaml.NewSourceFromString(tc.shared, niceyaml.WithName("shared.yaml"))
			base := yamltest.FirstDocument(t, baseInput, niceyaml.WithName("base.yaml"))
			prod := yamltest.FirstDocument(t, tc.prod, niceyaml.WithName("prod.yaml"), niceyaml.WithReferences(shared))

			var cfg layerConfig

			err := niceyaml.NewLayers(base, prod).DecodeInto(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)
			assert.Equal(t, "shared.example.com", cfg.Server.Host)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			if tc.unresolved {
				require.ErrorIs(t, bound.Unresolved(), paths.ErrAlias)
			}
		})
	}
}

func TestLayers_Concurrent(t *testing.T) {
	t.Parallel()

	layers := niceyaml.NewLayers(layerNodes(t,
		"server:\n  host: example.com\n  port: 0\n",
		"server:\n  host: prod.example.com\n",
	)...)

	// The first calls race to merge the layers, and each reads the one
	// merged document.
	const calls = 8

	var wg sync.WaitGroup

	decoded, bound := make([]error, calls), make([]error, calls)
	docs, merged := make([]*niceyaml.Node, calls), make([]error, calls)

	for i := range calls {
		wg.Go(func() {
			docs[i], merged[i] = layers.Document()

			_, decoded[i] = layers.Decode[layerConfig](t.Context())

			bound[i] = layers.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("server", "host"))))
		})
	}

	wg.Wait()

	for i := range calls {
		require.NoError(t, merged[i])
		assert.Same(t, docs[0], docs[i])
		require.EqualError(t, decoded[i], "base.yaml:3:9: $.server.port: port must be at least 1")
		require.EqualError(t, bound[i], "prod.yaml:2:9: $.server.host: bad")
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
