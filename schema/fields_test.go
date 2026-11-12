package schema_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

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

	holdsEndpoint struct {
		Endpoint endpoint `json:"endpoint"`
		Timeout  int
	}

	holdsQuantity struct {
		Memory quantity `json:"memory"`
	}
)

// endpoint stands for a type of another package, whose fields the
// program cannot tag. A [niceyaml.CustomUnmarshaler] decodes it whole.
type endpoint struct {
	Host string
	Port int
}

// quantity stands for a type of another package that decodes itself
// through UnmarshalJSON, as [niceyaml.JSONUnmarshalers] turns on.
type quantity struct {
	Value int
	Unit  string
}

func (q *quantity) UnmarshalJSON(b []byte) error {
	var s string

	err := json.Unmarshal(b, &s)
	if err != nil {
		return err //nolint:wrapcheck // The test returns the error as it is.
	}

	q.Unit = strings.TrimLeft(s, "0123456789")

	v, err := strconv.Atoi(strings.TrimSuffix(s, q.Unit))
	if err != nil {
		return err //nolint:wrapcheck // The test returns the error as it is.
	}

	q.Value = v

	return nil
}

// decodeEndpoint decodes an [endpoint] from its "host:port" text.
var decodeEndpoint = niceyaml.CustomUnmarshaler(
	func(_ context.Context, e *endpoint, decode func(any) error) error {
		var s string

		err := decode(&s)
		if err != nil {
			return err
		}

		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return err //nolint:wrapcheck // The test returns the error as it is.
		}

		e.Host = host
		e.Port, err = strconv.Atoi(port)

		return err //nolint:wrapcheck // The test returns the error as it is.
	},
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
		check: func() error { return schema.CheckFields[T]() },
		gen: func(t *testing.T) *schema.Schema {
			t.Helper()

			generated, err := jsonschema.GenerateFor[T](t.Context())
			require.NoError(t, err)

			return schema.FromJSONSchema(jsonschema.MustCompile(generated))
		},
		decode: func(ctx context.Context, doc *niceyaml.Node) error {
			_, err := doc.Decode[T](ctx, niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))

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

func TestCheckFieldsUnmarshalers(t *testing.T) {
	t.Parallel()

	endpointWant := []string{
		`holdsEndpoint.Timeout: field needs json:"timeout", because gen names it "Timeout" ` +
			`and the decode reads "timeout"`,
		`endpoint.Host: field needs json:"host", because gen names it "Host" and the decode reads "host"`,
		`endpoint.Port: field needs json:"port", because gen names it "Port" and the decode reads "port"`,
	}
	timeoutWant := endpointWant[:1]
	quantityWant := []string{
		`quantity.Value: field needs json:"value", because gen names it "Value" and the decode reads "value"`,
		`quantity.Unit: field needs json:"unit", because gen names it "Unit" and the decode reads "unit"`,
	}

	tcs := map[string]struct {
		check func(us ...niceyaml.TypeRule) error
		us    []niceyaml.TypeRule
		want  []string
	}{
		"type without an unmarshaler": {
			check: schema.CheckFields[holdsEndpoint],
			want:  endpointWant,
		},
		"custom unmarshaler": {
			check: schema.CheckFields[holdsEndpoint],
			us:    []niceyaml.TypeRule{decodeEndpoint},
			want:  timeoutWant,
		},
		"nil function removes a custom unmarshaler": {
			check: schema.CheckFields[holdsEndpoint],
			us:    []niceyaml.TypeRule{decodeEndpoint, niceyaml.CustomUnmarshaler[endpoint](nil)},
			want:  endpointWant,
		},
		"UnmarshalJSON methods off": {
			check: schema.CheckFields[holdsQuantity],
			want:  quantityWant,
		},
		"UnmarshalJSON methods on": {
			check: schema.CheckFields[holdsQuantity],
			us:    []niceyaml.TypeRule{niceyaml.JSONUnmarshalers(true)},
		},
		"last JSONUnmarshalers applies": {
			check: schema.CheckFields[holdsQuantity],
			us:    []niceyaml.TypeRule{niceyaml.JSONUnmarshalers(true), niceyaml.JSONUnmarshalers(false)},
			want:  quantityWant,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.check(tc.us...)
			if len(tc.want) == 0 {
				require.NoError(t, err)

				return
			}

			want := make([]string, 0, len(tc.want))
			for _, msg := range tc.want {
				want = append(want, schema.ErrFieldMismatch.Error()+": "+msg)
			}

			require.ErrorIs(t, err, schema.ErrFieldMismatch)
			require.EqualError(t, err, strings.Join(want, "\n"))
		})
	}
}

func TestCheckFieldsUnmarshalersMatchDecode(t *testing.T) {
	t.Parallel()

	us := []niceyaml.TypeRule{decodeEndpoint, niceyaml.JSONUnmarshalers(true)}

	type config struct {
		Endpoint endpoint `json:"endpoint"`
		Memory   quantity `json:"memory"`
	}

	require.NoError(t, schema.CheckFields[config](us...))

	doc := yamltest.FirstDocument(t, "endpoint: a.test:80\nmemory: 64Mi\n")

	got, err := doc.Decode[config](t.Context(),
		niceyaml.OverrideTypeRules(us...),
		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
	)
	require.NoError(t, err)
	require.Equal(t, config{
		Endpoint: endpoint{Host: "a.test", Port: 80},
		Memory:   quantity{Value: 64, Unit: "Mi"},
	}, got)
}

// typeConfig is the type of [TestCheckType]. Gen requires each field
// without omitzero.
type typeConfig struct {
	Kitchen typePrinter   `json:"kitchen"`
	Spare   *typePrinter  `json:"spare,omitzero"`
	Name    string        `json:"name"`
	Old     string        `json:"old"             jsonschema:"deprecated=true"`
	Seats   int           `json:"seats"`
	Tables  []typePrinter `json:"tables,omitzero"`
}

// typePrinter is a printer of [typeConfig].
type typePrinter struct {
	Port int `json:"port"`
}

func TestCheckType(t *testing.T) {
	t.Parallel()

	generated := func(t *testing.T) *schema.Schema {
		t.Helper()

		doc, err := jsonschema.GenerateFor[typeConfig](t.Context())
		require.NoError(t, err)

		return schema.FromJSONSchema(jsonschema.MustCompile(doc))
	}

	written := func(text string) func(t *testing.T) *schema.Schema {
		return func(t *testing.T) *schema.Schema {
			t.Helper()

			s, err := schema.Compile(t.Context(), []byte(text))
			require.NoError(t, err)

			return s
		}
	}

	tcs := map[string]struct {
		schema   func(t *testing.T) *schema.Schema
		defaults []niceyaml.TypeRule
		want     []string
		marks    []error
	}{
		"generated schema": {
			schema:   generated,
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(c *typeConfig) { c.Seats = 20 })},
			want: []string{
				"$.old: required and deprecated",
				"$.seats: required, so its default 20 never applies",
			},
			marks: []error{schema.ErrUnfollowableDeprecation, schema.ErrUnusedDefault},
		},
		"optional with a default": {
			schema:   written(`{"properties": {"seats": {"type": "integer"}}}`),
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(c *typeConfig) { c.Seats = 20 })},
		},
		"required with a zero default": {
			schema: written(`{"required": ["seats"]}`),
		},
		"required struct that merges": {
			schema:   written(`{"required": ["kitchen"]}`),
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(c *typeConfig) { c.Kitchen.Port = 9100 })},
		},
		"default that a parent sets": {
			schema: written(`{"properties": {"kitchen": {"$ref": "#/$defs/printer"}},
  "$defs": {"printer": {"required": ["port"]}}}`),
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(c *typeConfig) { c.Kitchen.Port = 9101 })},
			want: []string{
				stringtest.JoinLF(
					"$.kitchen.port: required, so its default 9101 never applies",
					"`-- typePrinter defaults port to 0 on its own, so omitzero makes port optional with 0 at its other places",
				),
			},
			marks: []error{schema.ErrUnusedDefault},
		},
		"default of an item": {
			schema:   written(`{"properties": {"tables": {"items": {"required": ["port"]}}}}`),
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(p *typePrinter) { p.Port = 9100 })},
			want:     []string{"$.tables[*].port: required, so its default 9100 never applies"},
			marks:    []error{schema.ErrUnusedDefault},
		},
		"default of a nil pointer": {
			schema:   written(`{"properties": {"spare": {"anyOf": [{"type": "null"}, {"required": ["port"]}]}}}`),
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(p *typePrinter) { p.Port = 9100 })},
			want:     []string{"$.spare.port: required, so its default 9100 never applies"},
			marks:    []error{schema.ErrUnusedDefault},
		},
		"required under a branch": {
			schema:   written(`{"anyOf": [{"required": ["seats"]}, {"required": ["name"]}]}`),
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(c *typeConfig) { c.Seats = 20 })},
		},
		"deprecated behind a $ref": {
			schema: written(`{"required": ["old"], "properties": {"old": {"$ref": "#/$defs/old"}},
  "$defs": {"old": {"deprecated": true}}}`),
			want:  []string{"$.old: required and deprecated"},
			marks: []error{schema.ErrUnfollowableDeprecation},
		},
		"deprecated behind the anyOf of a pointer": {
			schema: written(`{"required": ["old"],
  "properties": {"old": {"anyOf": [{"type": "null"}, {"deprecated": true}]}}}`),
			want:  []string{"$.old: required and deprecated"},
			marks: []error{schema.ErrUnfollowableDeprecation},
		},
		"deprecated under a branch": {
			schema: written(`{"required": ["old"],
  "properties": {"old": {"anyOf": [{"type": "string"}, {"deprecated": true}]}}}`),
		},
		"deprecated and optional": {
			schema: written(`{"properties": {"old": {"deprecated": true}}}`),
		},
		"deprecated root": {
			schema: written(`{"deprecated": true, "properties": {"name": {"type": "string"}}}`),
		},
		"accept all": {
			schema:   func(*testing.T) *schema.Schema { return schema.AcceptAll() },
			defaults: []niceyaml.TypeRule{niceyaml.TypeDefaults(func(c *typeConfig) { c.Seats = 20 })},
		},
		"nil schema": {
			schema: func(*testing.T) *schema.Schema { return nil },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := schema.CheckType[typeConfig](tc.schema(t), tc.defaults...)
			if len(tc.want) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			require.False(t, niceyaml.IsInvalid(err))

			var (
				rows  []string
				marks []error
			)

			for p := range niceyaml.Problems(err) {
				rows = append(rows, niceyaml.FormatError(p.Err))

				for _, mark := range []error{schema.ErrUnfollowableDeprecation, schema.ErrUnusedDefault} {
					if errors.Is(p.Err, mark) {
						marks = append(marks, mark)
					}
				}
			}

			assert.Equal(t, tc.want, rows)
			assert.Equal(t, tc.marks, marks)
		})
	}
}
