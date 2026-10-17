package manifest_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

func TestRegistry_Verify(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		schema []byte
		err    string
	}{
		"schema compiles": {
			schema: []byte(itemSchema),
		},
		"schema does not compile": {
			schema: []byte(`{"type": 5}`),
			err:    "MenuItem v1: ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("v1", manifest.Schema(schema.Embedded(tc.schema))),
			)))

			err := reg.Verify(t.Context())
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, schema.ErrCompile)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestRegistry_Verify_TypeFields(t *testing.T) {
	t.Parallel()

	required := func(p string) string {
		return stringtest.JoinLF(
			"MenuItem v1: "+p+": required, but no properties keyword declares it",
			"`-- declare "+p+" under properties, or remove it from required",
		)
	}

	unsure := func(p string) string {
		return stringtest.JoinLF(
			"MenuItem v1: "+p+": a branch or a $ref to another document might read it",
			"`-- declare "+p+" under properties outside every branch and every such $ref",
		)
	}

	tags := paths.Doc().Child("tags").Index(1).Child("kind")

	tcs := map[string]struct {
		opts   []manifest.RegistryOption
		schema string
		// The other files beside the schema, which a $ref of the schema
		// can name.
		files map[string]string
		// The schema belongs to the unversioned layout, and v1 has none.
		unversioned bool
		want        string
	}{
		"declared and required": {
			schema: brewSchema("v1", "MenuItem"),
		},
		"declared behind a $ref": {
			schema: `{"$ref": "#/$defs/item", "$defs": {"item": {
  "required": ["kind"],
  "properties": {"kind": {"type": "string"}}}}}`,
		},
		"required and not declared": {
			schema: `{"required": ["kind"], "properties": {"metadata": {"type": "object"}}}`,
			want:   required("$.kind"),
		},
		"required behind allOf and not declared": {
			schema: `{"allOf": [{"required": ["apiVersion"]}]}`,
			want:   required("$.apiVersion"),
		},
		"one of the type fields declared": {
			schema: `{"required": ["apiVersion", "kind"], "properties": {"apiVersion": {"type": "string"}}}`,
			want:   required("$.kind"),
		},
		"type field below the root": {
			opts:   []manifest.RegistryOption{manifest.WithKindField(meta.Child("kind"))},
			schema: `{"properties": {"metadata": {"required": ["kind"]}}}`,
			want:   required("$.metadata.kind"),
		},
		"type field below an index": {
			opts:   []manifest.RegistryOption{manifest.WithKindField(tags)},
			schema: `{"properties": {"tags": {"items": {"required": ["kind"]}}}}`,
			want:   required("$.tags[1].kind"),
		},
		"type field declared by prefixItems": {
			opts: []manifest.RegistryOption{manifest.WithKindField(tags)},
			schema: `{"properties": {"tags": {"prefixItems": [{}, {
  "required": ["kind"],
  "properties": {"kind": {"type": "string"}}}]}}}`,
		},
		"type field required by additionalItems": {
			opts: []manifest.RegistryOption{manifest.WithKindField(tags)},
			schema: `{"$schema": "http://json-schema.org/draft-07/schema#",
  "properties": {"tags": {"items": [{}], "additionalItems": {"required": ["kind"]}}}}`,
			want: required("$.tags[1].kind"),
		},
		"required in a branch": {
			schema: `{"oneOf": [{"required": ["kind"]}, {"required": ["metadata"]}]}`,
			want:   unsure("$.kind"),
		},
		"declared in a branch": {
			schema: `{"if": {"properties": {"kind": {"const": "MenuItem"}}}, "then": {"required": ["metadata"]}}`,
			want:   unsure("$.kind"),
		},
		"declared outside the branch that reads it": {
			schema: `{"properties": {"kind": {"type": "string"}},
  "if": {"properties": {"kind": {"const": "MenuItem"}}}, "then": {"required": ["metadata"]}}`,
		},
		"closed branches that leave the type fields out": {
			schema: `{"oneOf": [
  {"required": ["metadata"], "properties": {"metadata": {}}, "additionalProperties": false},
  {"required": ["spec"], "properties": {"spec": {}}, "additionalProperties": false}]}`,
		},
		"$ref to another document": {
			schema: `{"$ref": "defs.json#/$defs/item", "properties": {"apiVersion": {"type": "string"}}}`,
			files: map[string]string{"defs.json": `{"$defs": {"item": {
  "required": ["kind"],
  "properties": {"kind": {"type": "string"}}}}}`},
			want: unsure("$.kind"),
		},
		"unversioned layout": {
			schema:      `{"required": ["kind"]}`,
			unversioned: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{"schema.json": {Data: []byte(tc.schema)}}
			for path, text := range tc.files {
				fsys[path] = &fstest.MapFile{Data: []byte(text)}
			}

			sch := manifest.Schema(schema.FileFS(fsys, "schema.json"))

			versions := []manifest.KindOption{manifest.Version("v1", sch)}
			if tc.unversioned {
				versions = []manifest.KindOption{manifest.Unversioned(sch), manifest.Version("v1")}
			}

			reg := manifest.MustNewRegistry(append(tc.opts,
				manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem", versions...)))...)

			err := reg.Verify(t.Context())
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.want, niceyaml.FormatError(err))
			assert.False(t, niceyaml.IsInvalid(err))
		})
	}
}

func TestRegistry_Verify_Deprecations(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The schema of the written version v1, or "" for a kind whose
		// current version v2 is the written one.
		written string
		current string
		want    string
	}{
		"deprecated and optional": {
			current: `{"properties": {"spec": {"properties": {"legacyPricing": {"deprecated": true}}}}}`,
		},
		"required and deprecated": {
			current: `{"properties": {"spec": {
  "required": ["legacyPricing"],
  "properties": {"legacyPricing": {"deprecated": true}}}}}`,
			want: "MenuItem v2: $.spec.legacyPricing: required and deprecated",
		},
		"deprecated behind a $ref": {
			current: `{"properties": {"spec": {
  "required": ["legacyPricing"],
  "properties": {"legacyPricing": {"$ref": "#/$defs/pricing"}}}},
  "$defs": {"pricing": {"deprecated": true}}}`,
			want: "MenuItem v2: $.spec.legacyPricing: required and deprecated",
		},
		"required behind allOf": {
			current: `{"properties": {"spec": {
  "allOf": [{"required": ["legacyPricing"]}],
  "properties": {"legacyPricing": {"allOf": [{"deprecated": true}]}}}}}`,
			want: "MenuItem v2: $.spec.legacyPricing: required and deprecated",
		},
		"deprecated under a branch": {
			current: `{"properties": {"spec": {
  "required": ["legacyPricing"],
  "properties": {"legacyPricing": {"anyOf": [{"deprecated": true}, {"type": "string"}]}}}}}`,
		},
		"sibling of a $ref under Draft-07": {
			current: `{"$schema": "http://json-schema.org/draft-07/schema#",
  "properties": {"spec": {
    "required": ["legacyPricing"],
    "properties": {"legacyPricing": {"$ref": "#/definitions/pricing", "deprecated": true}}}},
  "definitions": {"pricing": {"type": "boolean"}}}`,
		},
		"recursive type": {
			current: `{"$ref": "#/$defs/node", "$defs": {"node": {
  "required": ["next"],
  "properties": {"next": {"$ref": "#/$defs/node"}, "old": {"deprecated": true}}}}}`,
		},
		"root": {
			current: `{"deprecated": true}`,
			want: stringtest.JoinLF(
				"MenuItem v2: $: the root schema is deprecated",
				"`-- use manifest.Deprecated instead",
			),
		},
		"root behind a $ref": {
			current: `{"$ref": "#/$defs/item", "$defs": {"item": {"deprecated": true}}}`,
			want: stringtest.JoinLF(
				"MenuItem v2: $: the root schema is deprecated",
				"`-- use manifest.Deprecated instead",
			),
		},
		"deprecated where the written version requires it": {
			written: `{"properties": {"spec": {"required": ["notes"]}}}`,
			current: `{"properties": {"spec": {"properties": {"notes": {"deprecated": true}}}}}`,
			want: stringtest.JoinLF(
				"MenuItem v2: $.spec.notes: deprecated, but v1 requires it",
				"`-- tag the field jsonschema:\"deprecated=false\" until manifest.Written leaves v1",
			),
		},
		"deprecated where the written version leaves it optional": {
			written: `{"properties": {"spec": {"properties": {"notes": {"type": "string"}}}}}`,
			current: `{"properties": {"spec": {"properties": {"notes": {"deprecated": true}}}}}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first := []manifest.VersionOption{}
			second := []manifest.VersionOption{manifest.Schema(schema.Embedded([]byte(tc.current)))}

			if tc.written != "" {
				first = append(first, manifest.Written(), manifest.Schema(schema.Embedded([]byte(tc.written))))
			}

			reg := manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("v1", first...),
				manifest.Version("v2", second...),
			)))

			err := reg.Verify(t.Context())
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.want, niceyaml.FormatError(err))
			assert.False(t, niceyaml.IsInvalid(err))
		})
	}
}

// verifyCafe is the kind of the tests of the default check.
type verifyCafe struct {
	Spec verifySpec `json:"spec"`
}

// verifySpec describes the café of the tests of the default check.
type verifySpec struct {
	Spare    *verifyPrinter           `json:"spare,omitzero"`
	Printers map[string]verifyPrinter `json:"printers,omitzero"`
	Currency string                   `json:"currency,omitzero"`
	Stations []verifyPrinter          `json:"stations,omitzero"`
	Kitchen  verifyPrinter            `json:"kitchen"`
	Seats    int                      `json:"seats,omitzero"`
}

// verifyPrinter is a printer of the tests of the default check.
type verifyPrinter struct {
	Port int `json:"port,omitzero"`
}

func TestRegistry_Verify_Defaults(t *testing.T) {
	t.Parallel()

	const printer = `"$defs": {"printer": {"type": "object", "required": ["port"]}}`

	tcs := map[string]struct {
		defaults []niceyaml.DecodeOption
		schema   string
		want     string
	}{
		"required with a default": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(s *verifySpec) { s.Seats = 20 })},
			schema:   `{"properties": {"spec": {"required": ["seats", "currency"]}}}`,
			want:     "Cafe v1: $.spec.seats: required, so its default 20 never applies",
		},
		"optional with a default": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(s *verifySpec) { s.Seats = 20 })},
			schema:   `{"properties": {"spec": {"properties": {"seats": {"type": "integer"}}}}}`,
		},
		"required struct that merges": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(s *verifySpec) { s.Kitchen.Port = 1 })},
			schema:   `{"properties": {"spec": {"required": ["kitchen"]}}}`,
		},
		"default that a parent sets": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(s *verifySpec) { s.Kitchen.Port = 9101 })},
			schema: `{"properties": {"spec": {"properties": {
  "kitchen": {"$ref": "#/$defs/printer"},
  "stations": {"items": {"$ref": "#/$defs/printer"}}}}}, ` + printer + `}`,
			want: stringtest.JoinLF(
				"Cafe v1: $.spec.kitchen.port: required, so its default 9101 never applies",
				"`-- verifyPrinter defaults port to 0 on its own, so omitzero makes port optional with 0 at its other places",
			),
		},
		"default of an item and a map value": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(p *verifyPrinter) { p.Port = 9100 })},
			schema: `{"properties": {"spec": {"properties": {
  "stations": {"items": {"$ref": "#/$defs/printer"}},
  "printers": {"additionalProperties": {"$ref": "#/$defs/printer"}}}}}, ` + printer + `}`,
			want: stringtest.JoinLF(
				"Cafe v1: $.spec.printers.*.port: required, so its default 9100 never applies",
				"Cafe v1: $.spec.stations[*].port: required, so its default 9100 never applies",
			),
		},
		"pointer that a parent sets": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(s *verifySpec) {
				s.Spare = &verifyPrinter{Port: 9200}
			})},
			schema: `{"properties": {"spec": {"properties": {
  "spare": {"anyOf": [{"$ref": "#/$defs/printer"}, {"type": "null"}]}}}}, ` + printer + `}`,
			want: stringtest.JoinLF(
				"Cafe v1: $.spec.spare.port: required, so its default 9200 never applies",
				"`-- verifyPrinter defaults port to 0 on its own, so omitzero makes port optional with 0 at its other places",
			),
		},
		"required under a branch": {
			defaults: []niceyaml.DecodeOption{niceyaml.WithDefaults(func(s *verifySpec) { s.Seats = 20 })},
			schema:   `{"properties": {"spec": {"anyOf": [{"required": ["seats"]}, {"required": ["currency"]}]}}}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[verifyCafe]("Cafe",
				manifest.DecodeOptions(tc.defaults...),
				manifest.Version("v1", manifest.Schema(schema.Embedded([]byte(tc.schema)))),
			)))

			err := reg.Verify(t.Context())
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			var rows []string

			for e := range niceyaml.Problems(err) {
				rows = append(rows, niceyaml.FormatError(e.Err))
			}

			assert.Equal(t, tc.want, stringtest.JoinLF(rows...))
		})
	}
}
