package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestHasValue(t *testing.T) {
	t.Parallel()

	hint := []string{`YAML reads an unquoted "#" as the start of a comment`}

	tcs := map[string]struct {
		input   string
		want    string
		path    string
		details []string
	}{
		"quoted color": {
			input: `bg: "#2b1d14"`,
		},
		"quoted empty string": {
			input: `bg: ""`,
		},
		"unquoted color": {
			input:   `bg: #2b1d14`,
			want:    "value is empty",
			path:    "$.bg",
			details: hint,
		},
		"bare key": {
			input: `bg:`,
			want:  "value is empty",
			path:  "$.bg",
		},
		"explicit null": {
			input: `bg: ~`,
			want:  "value is empty",
			path:  "$.bg",
		},
		"null with a comment": {
			input: `bg: ~ # #2b1d14`,
			want:  "value is empty",
			path:  "$.bg",
		},
		"unquoted token": {
			input:   "tokens:\n  nameTag: #e0a458",
			want:    "value is empty",
			path:    "$.tokens.nameTag",
			details: hint,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("name: fika\n" + tc.input + "\n")
			rule := niceyaml.WithValidator(niceyaml.Each(
				paths.Current().RecursiveAll(),
				niceyaml.ValidatorFunc(hasValue),
			))

			_, err := source.Decode[theme.Palette](t.Context(), rule)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			problem := yamltest.OnlyProblem(t, err)
			assert.Equal(t, tc.want, problem.Message())
			assert.Equal(t, tc.path, yamltest.PathOf(t, yamltest.RequireError(t, err)).String())

			var details []string

			for _, d := range yamltest.DetailsOf(err) {
				details = append(details, d.Message())
			}

			assert.Equal(t, tc.details, details)
		})
	}
}

func TestKnownKind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"predefined kind":              {input: `nameTag: "bold"`},
		"misspelled kind":              {input: `literalStrin: "#a3c585"`, want: "$.tokens.literalStrin~"},
		"kind below a predefined kind": {input: `genericHighlight.match: "bold"`},
		"kind below a misspelled kind": {
			input: `genericHighlite.match: "bold"`,
			want:  "$.tokens.'genericHighlite.match'~",
		},
		"custom kind": {input: `mine: "bold"`, want: "$.tokens.mine~"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("tokens:\n  " + tc.input + "\n")
			rule := niceyaml.WithValidator(niceyaml.Each(
				paths.Current().Child("tokens").ChildAll().Key(),
				niceyaml.ValidatorFunc(knownKind),
			))

			_, err := source.Decode[theme.Palette](t.Context(), rule)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			problem := yamltest.OnlyProblem(t, err)
			assert.Equal(t, "unknown kind", problem.Message())
			assert.Equal(t, tc.want, yamltest.PathOf(t, yamltest.RequireError(t, err)).String())
		})
	}
}

func TestDecodes(t *testing.T) {
	t.Parallel()

	// The rule reports the problems of the decode beside those of the
	// other rules, which otherwise end the decode before the decoder runs.
	source := niceyaml.NewSourceFromString(exampleBroken, niceyaml.WithName("broken.yaml"))
	rules := niceyaml.WithValidator(niceyaml.MultiValidator(
		niceyaml.ValidatorFunc(decodes),
		niceyaml.Each(paths.Current().RecursiveAll(), niceyaml.ValidatorFunc(hasValue)),
	))

	_, err := source.Decode[theme.Palette](t.Context(), rules, strict)
	require.ErrorIs(t, err, theme.ErrUnknownMode)

	for _, want := range []string{
		"$.accent: value is empty",
		`$.mode: unknown mode "dusk", want light or dark`,
		`$.bg: invalid color: "#2b1d1"`,
		`$.tokens.nameTag: unknown keyword: "bolder"`,
		`$.tokens.literalNumber: invalid color: "#d0877"`,
	} {
		require.ErrorContains(t, err, want)
	}

	// Without the rule, the failure of hasValue ends the decode before the
	// decoder reads the mode.
	_, err = source.Decode[theme.Palette](t.Context(),
		niceyaml.WithValidator(niceyaml.Each(paths.Current().RecursiveAll(), niceyaml.ValidatorFunc(hasValue))),
		strict,
	)
	require.Error(t, err)
	require.NotErrorIs(t, err, theme.ErrUnknownMode)
}
