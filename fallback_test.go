package niceyaml_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func ExampleWithFallback() {
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

	var cfg fallbackConfig

	err = base.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false))
	if err != nil {
		log.Fatal(err)
	}

	// The port of the server comes from base.yaml, and the list of servers
	// from prod.yaml.
	err = prod.DecodeInto(ctx, &cfg, niceyaml.WithFallback(base))
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

func TestWithFallback(t *testing.T) {
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
		change func(cfg *fallbackConfig)
		// The error without the option, which binds in the top layer.
		without string
		// The error with the layers below the top one as the fallback.
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
		// Two limits that the option documents.
		"a value the caller set binds at what a file holds": {
			layers: []string{"server:\n  host: example.com\n  port: 8080\n", "server:\n  host: x\n"},
			change: func(cfg *fallbackConfig) {
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

			var cfg fallbackConfig

			nodes := decodeLayers(t, &cfg, tc.layers...)
			top, below := nodes[0], nodes[1:]

			cfg.odd = tc.odd

			if tc.change != nil {
				tc.change(&cfg)
			}

			err := top.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.without)

			err = top.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(below...))
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestWithFallback_Decode(t *testing.T) {
	t.Parallel()

	const (
		baseInput = "server:\n  host: example.com\n  port: 0\n"
		prodInput = "server:\n  host: prod.example.com\n"

		want = "base.yaml:3:9: $.server.port: port must be at least 1"
	)

	t.Run("a decode binds its self-validation in the layer below", func(t *testing.T) {
		t.Parallel()

		var cfg fallbackConfig

		base := decodeLayers(t, &cfg, baseInput)[0]
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"))

		err := prod.DecodeInto(t.Context(), &cfg, niceyaml.WithFallback(base))
		require.EqualError(t, err, want)
		assert.True(t, niceyaml.IsInvalid(err))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, base, bound.Node())
		assert.Same(t, base, bound.Document())
		assert.Same(t, base.Source(), bound.Source())

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, "$.server.port", path.String())
	})

	t.Run("each option appends to the Nodes before it", func(t *testing.T) {
		t.Parallel()

		var cfg fallbackConfig

		nodes := decodeLayers(t, &cfg, "server:\n  port: 0\n", baseInput, prodInput)
		top, mid, base := nodes[0], nodes[1], nodes[2]

		// Both layers below set the port, and the nearer one binds.
		err := top.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(mid), niceyaml.WithFallback(base))
		require.EqualError(t, err, "mid.yaml:3:9: $.server.port: port must be at least 1")

		err = top.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(base), niceyaml.WithFallback(mid))
		require.EqualError(t, err, "base.yaml:2:9: $.server.port: port must be at least 1")
	})

	t.Run("a nil Node adds nothing", func(t *testing.T) {
		t.Parallel()

		var cfg fallbackConfig

		nodes := decodeLayers(t, &cfg, baseInput, prodInput)
		top, base := nodes[0], nodes[1]

		err := top.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(nil, base, nil))
		require.EqualError(t, err, want)

		err = top.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(nil))
		require.EqualError(t, err, "prod.yaml:1:1: $.server.port: port must be at least 1")
	})

	t.Run("a layer that did not parse holds no value", func(t *testing.T) {
		t.Parallel()

		var cfg fallbackConfig

		nodes := decodeLayers(t, &cfg, baseInput, prodInput)
		top, base := nodes[0], nodes[1]

		docs := niceyaml.NewSourceFromString("server: [\n", niceyaml.WithName("mid.yaml")).AllDocuments()
		require.Len(t, docs, 1)
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)

		err := top.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(docs[0], base))
		require.EqualError(t, err, want)

		// The Node of the call holds no value either when its document did
		// not parse, so the error binds in the layer below it.
		err = docs[0].SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(base))
		require.EqualError(t, err, want)

		err = docs[0].SelfValidate(t.Context(), &cfg)
		require.EqualError(t, err, "mid.yaml: $.server.port: port must be at least 1")
	})

	t.Run("scoped Nodes bind the path as the Node of the call reads it", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t,
			"defaults:\n  server:\n    host: example.com\n    port: 0\n",
			niceyaml.WithName("base.yaml"),
		)
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"))

		baseServer := yamltest.At(t, base, paths.Current().Child("defaults", "server"))
		prodServer := yamltest.At(t, prod, paths.Current().Child("server"))

		var server fallbackServer

		err := baseServer.DecodeInto(t.Context(), &server, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		err = prodServer.DecodeInto(t.Context(), &server, niceyaml.WithFallback(baseServer))
		require.EqualError(t, err, "base.yaml:4:11: $.server.port: port must be at least 1")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, baseServer, bound.Node())
	})

	t.Run("a validator and the decode read the Node of the call alone", func(t *testing.T) {
		t.Parallel()

		var cfg fallbackConfig

		base := decodeLayers(t, &cfg, baseInput)[0]
		prod := yamltest.FirstDocument(t, "server:\n  port: many\n", niceyaml.WithName("prod.yaml"))

		var seen *niceyaml.Node

		record := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = n

			return nil
		})

		err := prod.DecodeInto(t.Context(), &cfg, niceyaml.WithValidator(record), niceyaml.WithFallback(base))
		require.EqualError(t, err, "prod.yaml:2:9: $.server.port: expected integer, got string")
		assert.Same(t, prod, seen)
	})
}

func TestWithFallback_Alias(t *testing.T) {
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

			var cfg fallbackConfig

			base := decodeLayers(t, &cfg, "server:\n  host: example.com\n  port: 80\n")[0]
			prod := yamltest.FirstDocument(t, tc.prod, niceyaml.WithName("prod.yaml"), niceyaml.WithReferences(shared))

			err := prod.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			err = prod.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)

			err = prod.SelfValidate(t.Context(), &cfg, niceyaml.WithFallback(base))
			require.EqualError(t, err, tc.err)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			require.ErrorIs(t, bound.Unresolved(), paths.ErrAlias)
		})
	}
}

// decodeLayers decodes each of inputs into v in order, with the walk
// off, as a program that layers its files does. It returns the root Node
// of each, the last one first, so the Nodes after the first are the
// layers below it, nearest first. The first input is base.yaml and the
// last is prod.yaml, with mid.yaml between them. A single input is
// base.yaml.
func decodeLayers(t *testing.T, v any, inputs ...string) []*niceyaml.Node {
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

		doc := yamltest.FirstDocument(t, input, niceyaml.WithName(name))

		err := doc.DecodeInto(t.Context(), v, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		nodes[len(inputs)-1-i] = doc
	}

	return nodes
}

// fallbackServer is a server that needs a port once it names a host, so
// a server no layer sets passes.
type fallbackServer struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func (s fallbackServer) Validate() error {
	if s.Host != "" && s.Port < 1 {
		return niceyaml.NewError("port must be at least 1", niceyaml.AtPath(paths.Current().Child("port")))
	}

	return nil
}

// fallbackNested holds a [fallbackServer] one struct down.
type fallbackNested struct {
	Inner fallbackServer `yaml:"inner"`
}

// fallbackLimits needs a timeout, which a null in a document sets to nil.
type fallbackLimits struct {
	Timeout *int `yaml:"timeout"`
}

func (l fallbackLimits) Validate() error {
	if l.Timeout == nil {
		return niceyaml.NewError("timeout is required", niceyaml.AtPath(paths.Current().Child("timeout")))
	}

	return nil
}

// fallbackRate is a rate that needs a limit.
type fallbackRate struct {
	Limit int `yaml:"limit"`
}

func (r fallbackRate) Validate() error {
	if r.Limit < 1 {
		return niceyaml.NewError("limit must be at least 1", niceyaml.AtPath(paths.Current().Child("limit")))
	}

	return nil
}

// fallbackSwitch decodes itself into a new value, so a second decode
// replaces it whole.
type fallbackSwitch struct {
	Mode string `yaml:"mode"`
	On   bool   `yaml:"on"`
}

func (s *fallbackSwitch) UnmarshalYAML(unmarshal func(any) error) error {
	type plain fallbackSwitch

	var decoded plain

	err := unmarshal(&decoded)
	if err != nil {
		return err
	}

	*s = fallbackSwitch(decoded)

	return nil
}

// FallbackCommon holds the field [fallbackConfig] inlines.
type FallbackCommon struct {
	Region string `yaml:"region"`
}

func (c FallbackCommon) Validate() error {
	if c.Region == "nowhere" {
		return niceyaml.NewError("unknown region", niceyaml.AtPath(paths.Current().Child("region")))
	}

	return nil
}

// fallbackConfig holds a field of each kind that a second decode into
// one value merges or replaces. The go-yaml decoder fills the fields in
// the order the struct declares them, and the alias case of
// [TestWithFallback] needs Backup to decode before Server.
type fallbackConfig struct {
	FallbackCommon `yaml:",inline"`

	Extra    any                       `yaml:"extra"`
	Backends map[string]fallbackServer `yaml:"backends"`
	Rates    map[float64]fallbackRate  `yaml:"rates"`
	Backup   *fallbackServer           `yaml:"backup"`
	Limits   *fallbackLimits           `yaml:"limits"`
	Name     string                    `yaml:"name"`
	Servers  []fallbackServer          `yaml:"servers"`
	Nested   fallbackNested            `yaml:"nested"`
	Server   fallbackServer            `yaml:"server"`
	Switch   fallbackSwitch            `yaml:"switch"`
	Pair     [2]fallbackServer         `yaml:"pair"`

	// The paths Validate reports, which a test sets after the decodes.
	odd []paths.Path
}

// Validate reports an error at each path in odd under one summary, as a
// parent that checks the values below it does.
func (c fallbackConfig) Validate() error {
	if len(c.odd) == 0 {
		return nil
	}

	errs := make([]error, 0, len(c.odd))
	for _, path := range c.odd {
		errs = append(errs, niceyaml.NewError("odd value", niceyaml.AtPath(path)))
	}

	return niceyaml.NewSummary("config checks", errs...)
}
