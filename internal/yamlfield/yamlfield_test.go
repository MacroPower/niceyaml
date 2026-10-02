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

// fields holds one field for each rule that [yamlfield.Name] and
// [yamlfield.ReadsAnchor] apply.
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

	Alias       embedded `yaml:",inline,alias"`
	AliasFirst  embedded `yaml:",alias,omitempty,inline"`
	AliasJSON   embedded `json:",inline,alias"` //nolint:staticcheck // go-yaml reads the options of a json tag.
	AliasPrefix embedded `yaml:",inline,aliases"`
	AliasNamed  embedded `yaml:",inline,alias=base"`
	AliasOnly   embedded `yaml:",alias"`
	AliasDash   embedded `yaml:"-"`
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

func TestReadsAnchor(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		field string
		want  bool
	}{
		"inline with alias":             {field: "Alias", want: true},
		"alias before inline":           {field: "AliasFirst", want: true},
		"json tag":                      {field: "AliasJSON", want: true},
		"option that starts with alias": {field: "AliasPrefix", want: true},
		"alias that names an anchor":    {field: "AliasNamed"},
		"alias without inline":          {field: "AliasOnly"},
		"inline without alias":          {field: "Inner"},
		"no options":                    {field: "Plain"},
		"skipped":                       {field: "AliasDash"},
		"unexported":                    {field: "private"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			field, ok := reflect.TypeFor[fields]().FieldByName(tc.field)
			require.True(t, ok, "no field %s", tc.field)

			assert.Equal(t, tc.want, yamlfield.ReadsAnchor(field))
		})
	}
}

func TestIgnoresMerges(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		want bool
	}{
		"alias with omitempty": {
			typ: reflect.TypeFor[struct {
				Base embedded `yaml:",inline,alias,omitempty"`
				B    int
			}](),
			want: true,
		},
		"options in another order": {
			typ: reflect.TypeFor[struct {
				Base embedded `yaml:",omitempty,alias,inline"`
			}](),
			want: true,
		},
		"json tag": {
			typ: reflect.TypeFor[struct {
				Base *embedded `json:",inline,alias,omitempty"` //nolint:staticcheck // go-yaml reads the options of a json tag.
			}](),
			want: true,
		},
		"alias without omitempty": {
			typ: reflect.TypeFor[struct {
				Base embedded `yaml:",inline,alias"`
			}](),
		},
		"omitempty without alias": {
			typ: reflect.TypeFor[struct {
				Base embedded `yaml:",inline,omitempty"`
			}](),
		},
		"alias that names an anchor": {
			typ: reflect.TypeFor[struct {
				Base embedded `yaml:",inline,alias=base,omitempty"`
			}](),
		},
		"alias without inline": {
			typ: reflect.TypeFor[struct {
				Base embedded `yaml:",alias,omitempty"`
			}](),
		},
		"skipped field": {
			typ: reflect.TypeFor[struct {
				base embedded `yaml:",inline,alias,omitempty"` //nolint:unused // Only reflect reads the field.
			}](),
		},
		"no fields": {
			typ: reflect.TypeFor[struct{}](),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamlfield.IgnoresMerges(tc.typ))
		})
	}
}

func TestOwnNames(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"embedded":  true,
		"plain":     true,
		"fromYAML":  true,
		"fromJSON":  true,
		"-":         true,
		"options":   true,
		"aliasonly": true,
	}

	assert.Equal(t, want, yamlfield.OwnNames(reflect.TypeFor[fields]()))
}
