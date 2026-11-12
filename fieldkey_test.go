package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// FieldKeyInner is a struct that [fieldKeyFields] embeds.
type FieldKeyInner struct {
	Value int
}

// fieldKeyEmbedded is an unexported struct that [fieldKeyFields] embeds.
type fieldKeyEmbedded struct { //nolint:unused // Only reflect reads the type.
	Count int
}

// fieldKeyFields holds one field for each rule that [niceyaml.FieldKey]
// applies.
type fieldKeyFields struct {
	FieldKeyInner    `yaml:",inline"`
	fieldKeyEmbedded //nolint:unused // Only reflect reads the field.

	Plain    int
	CamelKey int
	YAML     int           `json:"ignored"       yaml:"fromYAML"`
	JSON     int           `json:"fromJSON"`
	Options  int           `yaml:",omitempty"`
	JSONOpts int           `json:",omitempty"`
	Skipped  int           `yaml:"-"`
	JSONSkip int           `json:"-"`
	Dash     int           `yaml:"-,"`
	Named    FieldKeyInner `yaml:"named,inline"`
	Alias    FieldKeyInner `yaml:",inline,alias"`
	private  int           //nolint:unused // Only reflect reads the field.
}

func TestFieldKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		field      string
		wantKey    string
		wantInline bool
		wantOK     bool
	}{
		"an untagged field takes its lowercased name": {
			field:   "Plain",
			wantKey: "plain",
			wantOK:  true,
		},
		"a camel-case name lowercases every letter": {
			field:   "CamelKey",
			wantKey: "camelkey",
			wantOK:  true,
		},
		"the yaml tag wins over the json tag": {
			field:   "YAML",
			wantKey: "fromYAML",
			wantOK:  true,
		},
		"the json tag names a field without a yaml tag": {
			field:   "JSON",
			wantKey: "fromJSON",
			wantOK:  true,
		},
		"a yaml tag without a name keeps the lowercased name": {
			field:   "Options",
			wantKey: "options",
			wantOK:  true,
		},
		"a json tag without a name keeps the lowercased name": {
			field:   "JSONOpts",
			wantKey: "jsonopts",
			wantOK:  true,
		},
		"a yaml tag of a dash skips the field": {
			field: "Skipped",
		},
		"a json tag of a dash skips the field": {
			field: "JSONSkip",
		},
		"a dash before a comma names the key": {
			field:   "Dash",
			wantKey: "-",
			wantOK:  true,
		},
		"an embedded inline field": {
			field:      "FieldKeyInner",
			wantKey:    "fieldkeyinner",
			wantInline: true,
			wantOK:     true,
		},
		"an embedded field of an unexported type is read": {
			field:   "fieldKeyEmbedded",
			wantKey: "fieldkeyembedded",
			wantOK:  true,
		},
		"a named inline field": {
			field:      "Named",
			wantKey:    "named",
			wantInline: true,
			wantOK:     true,
		},
		"an inline field that reads an anchor": {
			field:      "Alias",
			wantKey:    "alias",
			wantInline: true,
			wantOK:     true,
		},
		"an unexported field is skipped": {
			field: "private",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			field, found := reflect.TypeFor[fieldKeyFields]().FieldByName(tc.field)
			require.True(t, found)

			key, inline, ok := niceyaml.FieldKey(field)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantInline, inline)

			if tc.wantOK {
				assert.Equal(t, tc.wantKey, key)
			}
		})
	}
}

func ExampleFieldKey() {
	type Limits struct {
		Burst int `json:"burst_size"`
	}

	type Server struct {
		Limits `yaml:",inline"`

		MaxConns int
	}

	type Config struct {
		Server Server `yaml:"server"`
	}

	// The closure returns the `@` path of the field that names reach
	// from t, one Go field name for each struct along the way. It asks
	// FieldKey for the key that the document holds.
	pathOf := func(t reflect.Type, names ...string) paths.Path {
		p := paths.Current()

		for _, name := range names {
			field, _ := t.FieldByName(name)

			key, inline, ok := niceyaml.FieldKey(field)
			if !ok {
				// The decoder never reads this field.
				return p
			}

			// An inline field's own fields sit beside its siblings.
			if !inline {
				p = p.Child(key)
			}

			t = field.Type
		}

		return p
	}

	// The check learns each failing value as a Go field.
	check := niceyaml.WithValueCheck(func(_ context.Context, s *Server) error {
		server := reflect.TypeFor[Server]()

		var errs []error

		if s.MaxConns < 1 {
			errs = append(errs, niceyaml.NewError("must be at least 1",
				niceyaml.AtPath(pathOf(server, "MaxConns"))))
		}

		if s.Burst < 1 {
			errs = append(errs, niceyaml.NewError("must be at least 1",
				niceyaml.AtPath(pathOf(server, "Limits", "Burst"))))
		}

		return errors.Join(errs...)
	})

	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"server:",
		"  maxconns: 0",
		"  burst_size: 0",
	), niceyaml.WithName("app.yaml"))

	_, err := source.Decode[Config](context.Background(), check)
	fmt.Println(err)

	// Output:
	// app.yaml:2:13: $.server.maxconns: must be at least 1
	// app.yaml:3:15: $.server.burst_size: must be at least 1
}
