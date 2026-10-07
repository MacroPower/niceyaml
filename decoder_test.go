package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestDecoder_SelfValidate(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		db:
		  host: localhost
		by_grade:
		  high: {url: http://h}
	`)

	rejectAll := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return errors.New("validator ran")
	})

	// Each case decodes the input with the walk off, applies its layer,
	// and runs SelfValidate through dec.
	tcs := map[string]struct {
		dec   *niceyaml.Decoder
		layer func(cfg *layeredGradedConfig)
		err   string
	}{
		"the walk runs with self-validation off": {
			dec: niceyaml.NewDecoder(gradeNames, niceyaml.WithSelfValidation(false)),
			err: "app.yaml:1:1: $.db.password: password is required",
		},
		"keys decode with the go-yaml options of the Decoder": {
			dec: niceyaml.NewDecoder(gradeNames),
			layer: func(cfg *layeredGradedConfig) {
				cfg.DB.Password = "s3cret"
				cfg.ByGrade[gradeHigh] = upstream{URL: "ftp://h"}
			},
			err: `app.yaml:4:15: $.by_grade.high.url: url "ftp://h" is not http`,
		},
		"no validator of the Decoder runs": {
			dec: niceyaml.NewDecoder(gradeNames, niceyaml.WithValidator(rejectAll)),
			layer: func(cfg *layeredGradedConfig) {
				cfg.DB.Password = "s3cret"
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc, err := niceyaml.NewSourceFromString(input, niceyaml.WithName("app.yaml")).Document()
			require.NoError(t, err)

			var cfg layeredGradedConfig

			dec := niceyaml.NewDecoder(gradeNames, niceyaml.WithSelfValidation(false))
			require.NoError(t, dec.DecodeInto(t.Context(), doc, &cfg))

			if tc.layer != nil {
				tc.layer(&cfg)
			}

			err = tc.dec.SelfValidate(t.Context(), doc, &cfg)
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.err)
		})
	}

	t.Run("an unchanged value reports what DecodeInto reports", func(t *testing.T) {
		t.Parallel()

		doc, err := niceyaml.NewSourceFromString(input, niceyaml.WithName("app.yaml")).Document()
		require.NoError(t, err)

		var decoded, validated layeredGradedConfig

		dec := niceyaml.NewDecoder(gradeNames)
		decodeErr := dec.DecodeInto(t.Context(), doc, &decoded)
		require.EqualError(t, decodeErr, "app.yaml:1:1: $.db.password: password is required")

		off := dec.With(niceyaml.WithSelfValidation(false))
		require.NoError(t, off.DecodeInto(t.Context(), doc, &validated))

		err = off.SelfValidate(t.Context(), doc, &validated)
		require.Error(t, err)
		require.Equal(t, decodeErr.Error(), err.Error())
	})

	t.Run("a nil target returns an error", func(t *testing.T) {
		t.Parallel()

		doc, err := niceyaml.NewSourceFromString(input, niceyaml.WithName("app.yaml")).Document()
		require.NoError(t, err)

		err = niceyaml.NewDecoder().SelfValidate(t.Context(), doc, nil)
		require.ErrorIs(t, err, niceyaml.ErrSelfValidateTarget)
		require.EqualError(t, err, "app.yaml: self-validation target is nil")
	})
}

func TestDecoder_WithFallback(t *testing.T) {
	t.Parallel()

	const (
		baseInput = "server:\n  host: example.com\n  port: 0\n"

		inBase = "base.yaml:3:9: $.server.port: port must be at least 1"
	)

	t.Run("a Decoder falls back for every Node it decodes", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t, baseInput, niceyaml.WithName("base.yaml"))
		dec := niceyaml.NewDecoder(niceyaml.WithFallback(base))

		// Each file layers over the one shared base.
		for _, name := range []string{"east.yaml", "west.yaml"} {
			var cfg fallbackConfig

			err := base.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			top := yamltest.FirstDocument(t, "server:\n  host: "+name+"\n", niceyaml.WithName(name))

			err = dec.DecodeInto(t.Context(), top, &cfg)
			require.EqualError(t, err, inBase)

			err = dec.SelfValidate(t.Context(), top, &cfg)
			require.EqualError(t, err, inBase)
		}
	})

	t.Run("With appends below the Nodes of the receiver and leaves it unchanged", func(t *testing.T) {
		t.Parallel()

		var cfg fallbackConfig

		nodes := decodeLayers(t, &cfg, baseInput, "server:\n  port: -1\n", "server:\n  host: prod.example.com\n")
		top, mid, base := nodes[0], nodes[1], nodes[2]

		plain := niceyaml.NewDecoder()
		overBase := plain.With(niceyaml.WithFallback(base))
		overBoth := overBase.With(niceyaml.WithFallback(mid))

		err := plain.SelfValidate(t.Context(), top, &cfg)
		require.EqualError(t, err, "prod.yaml:1:1: $.server.port: port must be at least 1")

		err = overBase.SelfValidate(t.Context(), top, &cfg)
		require.EqualError(t, err, inBase)

		// The base file stays nearest, so it binds before the middle one.
		err = overBoth.SelfValidate(t.Context(), top, &cfg)
		require.EqualError(t, err, inBase)

		err = plain.With(niceyaml.WithFallback(mid, base)).SelfValidate(t.Context(), top, &cfg)
		require.EqualError(t, err, "mid.yaml:2:9: $.server.port: port must be at least 1")
	})
}

func ExampleDecoder_SelfValidate() {
	ctx := context.Background()

	doc, err := niceyaml.NewSourceFromString("db:\n  host: localhost\n", niceyaml.WithName("app.yaml")).Document()
	if err != nil {
		log.Fatal(err)
	}

	dec := niceyaml.NewDecoder(niceyaml.WithSelfValidation(false))

	var cfg layeredConfig

	err = dec.DecodeInto(ctx, doc, &cfg)
	if err != nil {
		log.Fatal(err)
	}

	// The file leaves the password to the environment.
	fmt.Println(dec.SelfValidate(ctx, doc, &cfg))

	applyEnv(&cfg, map[string]string{"DB_PASSWORD": "s3cret"})

	fmt.Println(dec.SelfValidate(ctx, doc, &cfg))

	// Output:
	// app.yaml:1:1: $.db.password: password is required
	// <nil>
}

// layeredGradedConfig holds a database, which needs a password, and a
// map whose keys only gradeNames decodes.
type layeredGradedConfig struct {
	ByGrade map[grade]upstream `yaml:"by_grade"`
	DB      database           `yaml:"db"`
}

// applyEnv sets the fields of cfg that env names, as a program sets them
// from its environment.
func applyEnv(cfg *layeredConfig, env map[string]string) {
	if password, ok := env["DB_PASSWORD"]; ok {
		cfg.DB.Password = password
	}
}
