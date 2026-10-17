package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestReadableColor(t *testing.T) {
	t.Parallel()

	hint := []string{`YAML reads an unquoted "#" as the start of a comment`}

	tcs := map[string]struct {
		input   string
		want    string
		details []string
	}{
		"quoted color": {
			input: `bg: "#2b1d14"`,
		},
		"unquoted color": {
			input:   `bg: #2b1d14`,
			want:    "color is empty",
			details: hint,
		},
		"bare key": {
			input: `bg:`,
			want:  "color is empty",
		},
		"explicit null": {
			input: `bg: ~`,
			want:  "color is empty",
		},
		"null with a comment": {
			input: `bg: ~ # #2b1d14`,
			want:  "color is empty",
		},
		"quoted empty string": {
			input: `bg: ""`,
			want:  "color is empty",
		},
		"invalid color": {
			input: `bg: "#zz"`,
			want:  `invalid color: "#zz"`,
		},
		"sequence": {
			input: `bg: ["#fff"]`,
			want:  "decode color: expected string, got sequence",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("colors:\n  " + tc.input + "\n")
			rule := niceyaml.WithValidator(niceyaml.Each(
				paths.Current().Child("colors").ChildAll(),
				niceyaml.ValidatorFunc(readableColor),
			))

			_, err := source.Decode[themeFile](t.Context(), rule)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			problem := yamltest.OnlyProblem(t, err)
			assert.Equal(t, tc.want, problem.Message())

			var details []string

			for _, d := range yamltest.DetailsOf(err) {
				details = append(details, d.Message())
			}

			assert.Equal(t, tc.details, details)
		})
	}
}
