package schema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// Shared holds a field that the structs of [TestCheckFields] embed.
type Shared struct {
	Note string `json:"note"`
}

type (
	jsonNamed struct {
		Name string `json:"name"`
		Port int    `json:"port,omitempty"`
	}

	yamlNamed struct {
		ServerName string `yaml:"server_name"`
		Port       int
	}

	jsonOptions struct {
		Port int `json:"Port,omitempty" yaml:"port"`
	}

	tagsAgree struct {
		Port int `json:"port" yaml:"port,omitempty"`
	}

	yamlOptionsOnly struct {
		ServerPort int `json:"serverPort" yaml:",omitempty"`
	}

	dashName struct {
		Dash string `yaml:"-,"`
	}

	decoderSkips struct {
		Name  string `json:"name"`
		Cache string `yaml:"-"`
	}

	genSkips struct {
		Port int `json:"-" yaml:"port"`
	}

	bothSkip struct {
		Name  string `json:"name"`
		Cache string `json:"-"`
		cache string //nolint:unused // Only reflect reads the field.
	}

	embedsShared struct {
		Shared

		Price int `json:"price"`
	}

	inlinesShared struct {
		Shared `yaml:",inline"`

		Price int `json:"price"`
	}

	skipsShared struct {
		Shared `yaml:"-"`

		Price int `json:"price"`
	}

	namedInline struct {
		Extra Shared `yaml:",inline"`
	}

	jsonNamedInline struct {
		Shared `json:"shared" yaml:",inline"`
	}

	genSkipsInline struct {
		Shared `json:"-" yaml:",inline"`

		Price int `json:"price"`
	}

	embedOption struct {
		Extra Shared `json:",embed"`
	}

	embedOptionInline struct {
		Extra Shared `json:",embed" yaml:",inline"`
	}

	decoderSkipsEmbedOption struct {
		Extra Shared `json:",embed" yaml:"-"`

		Price int `json:"price"`
	}

	embedMap struct {
		Name  string            `json:"name"`
		Extra map[string]string `json:",embed"`
	}

	nested struct {
		Items map[string][]*yamlNamed `json:"items"`
	}

	holdsText struct {
		At text `json:"at"`
	}

	holdsProvided struct {
		Server provided `json:"server"`
	}
)

// provided supplies its own schema, which names its field as the decoder
// does.
type provided struct {
	ServerName string `yaml:"server_name"`
}

func (provided) JSONSchema(context.Context, jsonschema.TypeContext) (jsonschema.TypeSchema, error) {
	return jsonschema.TypeSchema{Value: &jsonschema.Schema{
		Type:       "object",
		Properties: map[string]*jsonschema.Schema{"server_name": {Type: "string"}},
		Required:   []string{"server_name"},
	}}, nil
}

// text decodes itself from a string, so its fields do not need tags.
type text struct {
	Value string
}

func (t text) MarshalText() ([]byte, error) {
	return []byte(t.Value), nil
}

func (t *text) UnmarshalText(b []byte) error {
	t.Value = string(b)

	return nil
}

// fieldsCase is a case of [TestCheckFields] for one type.
type fieldsCase struct {
	// The call of [schema.CheckFields] for the type.
	check func() error
	// The function that returns the schema that the generator writes for
	// the type.
	gen func(t *testing.T) *schema.Schema
	// The function that decodes doc into the type.
	decode func(ctx context.Context, doc *niceyaml.Node) error
	// A document that the type decodes.
	input string
	// A message for each field that the check reports, without the text of
	// [schema.ErrFieldMismatch].
	want []string
}

// fieldsCaseFor returns a [fieldsCase] for T.
func fieldsCaseFor[T any](input string, want ...string) fieldsCase {
	return fieldsCase{
		check: schema.CheckFields[T],
		gen: func(t *testing.T) *schema.Schema {
			t.Helper()

			generated, err := jsonschema.GenerateFor[T](t.Context())
			require.NoError(t, err)

			return schema.FromJSONSchema(jsonschema.MustCompile(generated))
		},
		decode: func(ctx context.Context, doc *niceyaml.Node) error {
			_, err := doc.Decode[T](ctx, niceyaml.WithDisallowUnknownFields(true))

			return err
		},
		input: input,
		want:  want,
	}
}

func TestCheckFields(t *testing.T) {
	t.Parallel()

	yamlNamedWant := []string{
		`yamlNamed.ServerName: field needs json:"server_name", because gen names it "ServerName" ` +
			`and the decode reads "server_name"`,
		`yamlNamed.Port: field needs json:"port", because gen names it "Port" and the decode reads "port"`,
	}

	tcs := map[string]fieldsCase{
		"json tags":                   fieldsCaseFor[jsonNamed]("name: a\nport: 1\n"),
		"yaml tag and untagged field": fieldsCaseFor[yamlNamed]("server_name: a\nport: 1\n", yamlNamedWant...),
		"json tag with options": fieldsCaseFor[jsonOptions]("port: 1\n",
			`jsonOptions.Port: field needs json:"port,omitempty", because gen names it "Port" `+
				`and the decode reads "port"`,
		),
		"yaml and json tags that agree": fieldsCaseFor[tagsAgree]("port: 1\n"),
		"yaml tag that holds only options": fieldsCaseFor[yamlOptionsOnly]("serverport: 1\n",
			`yamlOptionsOnly.ServerPort: field needs yaml:"serverPort,omitempty", because gen names it "serverPort" `+
				`and the decode reads "serverport"`,
		),
		"yaml name of a dash": fieldsCaseFor[dashName]("\"-\": a\n",
			`dashName.Dash: field needs a yaml name other than "-", because the decode reads "-" `+
				`and gen cannot name a property "-"`,
		),
		"field the decoder skips": fieldsCaseFor[decoderSkips]("name: a\n",
			`decoderSkips.Cache: field needs json:"-", because the decode skips it and gen names it "Cache"`,
		),
		"field gen leaves out": fieldsCaseFor[genSkips]("port: 1\n",
			`genSkips.Port: field needs json:"port", because gen leaves it out and the decode reads "port"`,
		),
		"field both leave out": fieldsCaseFor[bothSkip]("name: a\n"),
		"embedded struct without an inline option": fieldsCaseFor[embedsShared]("shared:\n  note: a\nprice: 1\n",
			`embedsShared.Shared: embedded struct needs yaml:",inline", because gen reads it inline `+
				`and the decode nests it`,
		),
		"embedded struct with an inline option": fieldsCaseFor[inlinesShared]("note: a\nprice: 1\n"),
		"embedded struct the decoder skips": fieldsCaseFor[skipsShared]("price: 1\n",
			`skipsShared.Shared: embedded struct needs json:"-", because the decode skips it and gen reads it inline`,
		),
		"named field with an inline option": fieldsCaseFor[namedInline]("note: a\n",
			`namedInline.Extra: named field has an inline option, which the decode reads inline and gen nests`,
		),
		"embedded field with a JSON name and an inline option": fieldsCaseFor[jsonNamedInline]("note: a\n",
			`jsonNamedInline.Shared: embedded field has a JSON name and an inline option, `+
				`which the decode reads inline and gen nests`,
		),
		"field gen leaves out with an inline option": fieldsCaseFor[genSkipsInline]("note: a\nprice: 1\n",
			`genSkipsInline.Shared: field has json:"-" and an inline option, `+
				`which the decode reads inline and gen leaves out`,
		),
		"embed option": fieldsCaseFor[embedOption]("extra:\n  note: a\n",
			`embedOption.Extra: field with the embed option needs yaml:",inline", because gen reads it inline `+
				`and the decode nests it`,
		),
		"embed option with an inline option": fieldsCaseFor[embedOptionInline]("note: a\n"),
		"embed option the decoder skips": fieldsCaseFor[decoderSkipsEmbedOption]("price: 1\n",
			`decoderSkipsEmbedOption.Extra: field with the embed option needs json:"-", `+
				`because the decode skips it and gen reads it inline`,
		),
		"embed option on a map": fieldsCaseFor[embedMap]("name: a\nextra:\n  b: c\n",
			`embedMap.Extra: field with the embed option needs json:"extra", because gen merges its entries `+
				`into the parent and the decode reads "extra"`,
		),
		"struct the decode reaches": fieldsCaseFor[nested](
			"items:\n  a:\n    - server_name: a\n      port: 1\n",
			yamlNamedWant...,
		),
		"type that decodes itself":          fieldsCaseFor[holdsText]("at: a\n"),
		"type that supplies its own schema": fieldsCaseFor[holdsProvided]("server:\n  server_name: a\n"),
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			require.NoError(t, tc.decode(t.Context(), doc), "the type decodes the input")

			err := tc.check()
			if len(tc.want) == 0 {
				require.NoError(t, err)
				require.NoError(t, doc.Validate(t.Context(), tc.gen(t)), "the generated schema accepts the input")

				return
			}

			require.ErrorIs(t, err, schema.ErrFieldMismatch)

			want := make([]string, 0, len(tc.want))
			for _, msg := range tc.want {
				want = append(want, schema.ErrFieldMismatch.Error()+": "+msg)
			}

			require.EqualError(t, err, strings.Join(want, "\n"))

			var joined interface{ Unwrap() []error }

			require.ErrorAs(t, err, &joined)

			for _, branch := range joined.Unwrap() {
				require.ErrorIs(t, branch, schema.ErrFieldMismatch)
			}

			require.Error(t, doc.Validate(t.Context(), tc.gen(t)), "the generated schema rejects the input")
		})
	}
}
