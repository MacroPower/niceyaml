package niceyaml_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkNode_Decode_SelfValidation decodes several shapes of document
// with and without self-validation, which scans the decoded value for
// validators and reads the keys of each map that holds one from the
// document. The values go-yaml decodes into any hold none, so the walk
// reads no keys for them. The shapes are a list of small mappings and a
// mapping that holds as many small mappings, both as any, and a mapping
// of strings and a list of ints, whose types hold no validator. A
// mapping of strings whose type validates itself holds no validator
// below it, so the walk reads none of its keys. A list of rows, each a
// struct of many scalar fields and one field that validates itself,
// shows what the walk spends on fields whose types hold no validator.
// The time per item should stay flat as each grows.
func BenchmarkNode_Decode_SelfValidation(b *testing.B) {
	decodeAny := func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
		_, err := doc.Decode[any](ctx, opts...)

		return err
	}

	shapes := []struct {
		decode func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error
		item   func(i int) string
		name   string
		header string
	}{
		{
			name:   "list",
			header: "items:\n",
			item:   func(int) string { return "  - {name: x, value: 1, tags: {a: 1}}\n" },
			decode: decodeAny,
		},
		{
			name:   "wide_map",
			item:   func(i int) string { return fmt.Sprintf("k%d: {a: 1}\n", i) },
			decode: decodeAny,
		},
		{
			name: "string_map",
			item: func(i int) string { return fmt.Sprintf("k%d: v%d\n", i, i) },
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[map[string]string](ctx, opts...)

				return err
			},
		},
		{
			name: "validated_string_map",
			item: func(i int) string { return fmt.Sprintf("k%d: v%d\n", i, i) },
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[labels](ctx, opts...)

				return err
			},
		},
		{
			name: "wide_rows",
			item: func(i int) string {
				return fmt.Sprintf(
					"- {a: a%d, b: b, c: c, d: d, e: e, f: 1, g: 2, h: 3, i: true, j: 1.5, port: 80}\n",
					i,
				)
			},
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[[]wideRow](ctx, opts...)

				return err
			},
		},
		{
			name: "int_list",
			item: func(i int) string { return fmt.Sprintf("- %d\n", i) },
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[[]int](ctx, opts...)

				return err
			},
		},
	}

	sizes := []int{1000, 4000, 16000}

	modes := []struct {
		name string
		opts []niceyaml.DecodeOption
	}{
		{"self_validation", nil},
		{"no_self_validation", []niceyaml.DecodeOption{niceyaml.WithSelfValidation(false)}},
	}

	for _, shape := range shapes {
		for _, items := range sizes {
			var sb strings.Builder

			sb.WriteString(shape.header)

			for i := range items {
				sb.WriteString(shape.item(i))
			}

			yaml := sb.String()

			doc := yamltest.FirstDocument(b, yaml)

			for _, mode := range modes {
				b.Run(fmt.Sprintf("%s_%d/%s", shape.name, items, mode.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(yaml)))

					for b.Loop() {
						err := shape.decode(b.Context(), doc, mode.opts)
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// wideRow is a struct of many scalar fields and one field that validates
// itself.
type wideRow struct {
	A    string  `yaml:"a"`
	B    string  `yaml:"b"`
	C    string  `yaml:"c"`
	D    string  `yaml:"d"`
	E    string  `yaml:"e"`
	F    int     `yaml:"f"`
	G    int     `yaml:"g"`
	H    int     `yaml:"h"`
	I    bool    `yaml:"i"`
	J    float64 `yaml:"j"`
	Port port    `yaml:"port"`
}
