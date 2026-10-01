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
// A list of rows that each hold an inline struct with a field that
// validates itself shows what the walk spends on inline fields. A list
// of rows that validate themselves and have a getter per field,
// like generated message types, shows what the walk spends on types
// with many methods. The time per item should stay flat as each grows.
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
			name: "inline_rows",
			item: func(i int) string { return fmt.Sprintf("- {name: n%d, port: 80}\n", i) },
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[[]inlineRow](ctx, opts...)

				return err
			},
		},
		{
			name: "method_rows",
			item: func(i int) string {
				return fmt.Sprintf("- {a: a%d, b: b, c: c, d: d, e: 1, f: 2, g: 3, h: true}\n", i)
			},
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[[]methodRow](ctx, opts...)

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

	for _, shape := range shapes {
		for _, items := range sizes {
			var sb strings.Builder

			sb.WriteString(shape.header)

			for i := range items {
				sb.WriteString(shape.item(i))
			}

			yaml := sb.String()

			doc := yamltest.FirstDocument(b, yaml)

			for _, mode := range selfValidationModes {
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

// BenchmarkNode_Decode_FirstDecode decodes a new parse of a document in
// each iteration, so it measures the work a document does once, on its
// first decode, which the other benchmarks spread over many decodes. The
// document holds a map and a list of small mappings, and its type
// validates each of them, so self-validation reads the keys of the map.
// The document holds no !!int tag, no reused anchor name, and no alias,
// so its first decode should cost about as much with self-validation as
// without it.
func BenchmarkNode_Decode_FirstDecode(b *testing.B) {
	type config struct {
		M     map[string]item `yaml:"m"`
		Items []item          `yaml:"items"`
	}

	var sb strings.Builder

	sb.WriteString("m:\n  k: {name: a, price: 1}\nitems:\n")

	for i := range 1000 {
		fmt.Fprintf(&sb, "  - {name: n%d, price: %d}\n", i, i)
	}

	yaml := sb.String()

	modes := []struct {
		name string
		opts []niceyaml.DecodeOption
	}{
		{"self_validation", nil},
		{"no_self_validation", []niceyaml.DecodeOption{niceyaml.WithSelfValidation(false)}},
	}

	for _, mode := range modes {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, yaml)

				b.StartTimer()

				_, err := doc.Decode[config](b.Context(), mode.opts...)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkNode_Decode_SelfValidation_Nested decodes deep documents with
// and without self-validation. Every level adds a map whose keys the walk
// reads from the document. In the tree, each node validates itself and
// holds its child in a map, so each map lies inside the map above it. In
// the chains, each link holds a map of ports beside the next link, which
// a struct field or a list element holds, so no map lies inside another.
// The walk resolves the node of each value at most once, from the node
// above it, so the time per level should stay close to flat as the
// document deepens. The decoder rejects a tree of 3000 levels, so the
// depths stop at 2000.
func BenchmarkNode_Decode_SelfValidation_Nested(b *testing.B) {
	shapes := []struct {
		decode      func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error
		name        string
		open, close string
	}{
		{
			name:  "map_tree",
			open:  "{kids: {a: ",
			close: "}}",
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[branch](ctx, opts...)

				return err
			},
		},
		{
			name:  "struct_chain",
			open:  "{ports: {a: 80}, next: ",
			close: "}",
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[structLink](ctx, opts...)

				return err
			},
		},
		{
			name:  "list_chain",
			open:  "{ports: {a: 80}, next: [",
			close: "]}",
			decode: func(ctx context.Context, doc *niceyaml.Node, opts []niceyaml.DecodeOption) error {
				_, err := doc.Decode[listLink](ctx, opts...)

				return err
			},
		},
	}

	for _, shape := range shapes {
		for _, depth := range []int{500, 1000, 2000} {
			yaml := strings.Repeat(shape.open, depth) + "{}" + strings.Repeat(shape.close, depth)

			doc := yamltest.FirstDocument(b, yaml)

			for _, mode := range selfValidationModes {
				b.Run(fmt.Sprintf("%s_%d/%s", shape.name, depth, mode.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(yaml)))

					for b.Loop() {
						err := shape.decode(b.Context(), doc, mode.opts)
						if err != nil {
							b.Fatal(err)
						}
					}

					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*depth), "ns/level")
				})
			}
		}
	}
}

// selfValidationModes holds the options that decode with and without
// self-validation.
var selfValidationModes = []struct {
	name string
	opts []niceyaml.DecodeOption
}{
	{"self_validation", nil},
	{"no_self_validation", []niceyaml.DecodeOption{niceyaml.WithSelfValidation(false)}},
}

// branch is a node of a tree that validates itself and holds its
// children in a map.
type branch struct {
	Kids map[string]*branch `yaml:"kids"`
}

func (*branch) Validate() error { return nil }

// structLink is a link of a chain that holds the next link in a struct
// field, beside a map of values that validate themselves.
type structLink struct {
	Next  *structLink     `yaml:"next"`
	Ports map[string]port `yaml:"ports"`
}

// listLink is a link of a chain that holds the next link in a list,
// beside a map of values that validate themselves.
type listLink struct {
	Ports map[string]port `yaml:"ports"`
	Next  []listLink      `yaml:"next"`
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

// inlineRow is a struct of a scalar field and an inline struct that holds
// a field that validates itself.
type inlineRow struct {
	Name  string         `yaml:"name"`
	Inner inlineRowInner `yaml:",inline"`
}

// inlineRowInner is the inline struct of an [inlineRow].
type inlineRowInner struct {
	Port port `yaml:"port"`
}

// methodRow is a struct that validates itself and has a getter for each
// field and the other methods of a generated message type.
type methodRow struct {
	A string `yaml:"a"`
	B string `yaml:"b"`
	C string `yaml:"c"`
	D string `yaml:"d"`
	E int    `yaml:"e"`
	F int    `yaml:"f"`
	G int    `yaml:"g"`
	H bool   `yaml:"h"`
}

func (*methodRow) Validate() error { return nil }

func (r *methodRow) GetA() string { return r.A }

func (r *methodRow) GetB() string { return r.B }

func (r *methodRow) GetC() string { return r.C }

func (r *methodRow) GetD() string { return r.D }

func (r *methodRow) GetE() int { return r.E }

func (r *methodRow) GetF() int { return r.F }

func (r *methodRow) GetG() int { return r.G }

func (r *methodRow) GetH() bool { return r.H }

func (r *methodRow) Reset() { *r = methodRow{} }

func (r *methodRow) String() string { return fmt.Sprintf("%+v", *r) }

func (*methodRow) ProtoMessage() {}
