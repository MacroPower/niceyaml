package yamlfield_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

type embedded struct {
	E int
}

// fields holds one field for each rule [yamlfield.Name] applies.
type fields struct {
	embedded //nolint:unused // Only reflect reads the field.

	Inner   embedded `yaml:",inline"`
	Plain   int
	YAML    int `json:"ignored"    yaml:"fromYAML"`
	JSON    int `json:"fromJSON"`
	Skipped int `yaml:"-"`
	Dash    int `yaml:"-,"`
	Options int `yaml:",omitempty"`
	private int //nolint:unused // Only reflect reads the field.
}

func TestName(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		field  string
		want   string
		inline bool
		skip   bool
	}{
		"lowercased field name":  {field: "Plain", want: "plain"},
		"yaml tag":               {field: "YAML", want: "fromYAML"},
		"json tag":               {field: "JSON", want: "fromJSON"},
		"dash tag":               {field: "Skipped", skip: true},
		"dash name with options": {field: "Dash", want: "-"},
		"options without a name": {field: "Options", want: "options"},
		"inline":                 {field: "Inner", want: "inner", inline: true},
		"unexported embedded":    {field: "embedded", want: "embedded"},
		"unexported":             {field: "private", skip: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			field, ok := reflect.TypeFor[fields]().FieldByName(tc.field)
			require.True(t, ok, "no field %s", tc.field)

			got, inline, skip := yamlfield.Name(field)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.inline, inline)
			assert.Equal(t, tc.skip, skip)
		})
	}
}

func TestOwnNames(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"embedded": true,
		"plain":    true,
		"fromYAML": true,
		"fromJSON": true,
		"-":        true,
		"options":  true,
	}

	assert.Equal(t, want, yamlfield.OwnNames(reflect.TypeFor[fields]()))
}
