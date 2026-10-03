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
