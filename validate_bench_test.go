package niceyaml_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
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
	shapes := []struct {
		decode func(opts ...niceyaml.DecodeOption) func(context.Context, *niceyaml.Node) error
		item   func(i int) string
		name   string
		header string
	}{
		{
			name:   "list",
			header: "items:\n",
			item:   func(int) string { return "  - {name: x, value: 1, tags: {a: 1}}\n" },
			decode: decodeAs[any],
		},
		{
			name:   "wide_map",
			item:   func(i int) string { return fmt.Sprintf("k%d: {a: 1}\n", i) },
			decode: decodeAs[any],
		},
		{
			name:   "string_map",
			item:   func(i int) string { return fmt.Sprintf("k%d: v%d\n", i, i) },
			decode: decodeAs[map[string]string],
		},
		{
			name:   "validated_string_map",
			item:   func(i int) string { return fmt.Sprintf("k%d: v%d\n", i, i) },
			decode: decodeAs[labels],
		},
		{
			name: "wide_rows",
			item: func(i int) string {
				return fmt.Sprintf(
					"- {a: a%d, b: b, c: c, d: d, e: e, f: 1, g: 2, h: 3, i: true, j: 1.5, port: 80}\n",
					i,
				)
			},
			decode: decodeAs[[]wideRow],
		},
		{
			name:   "inline_rows",
			item:   func(i int) string { return fmt.Sprintf("- {name: n%d, port: 80}\n", i) },
			decode: decodeAs[[]inlineRow],
		},
		{
			name: "method_rows",
			item: func(i int) string {
				return fmt.Sprintf("- {a: a%d, b: b, c: c, d: d, e: 1, f: 2, g: 3, h: true}\n", i)
			},
			decode: decodeAs[[]methodRow],
		},
		{
			name:   "int_list",
			item:   func(i int) string { return fmt.Sprintf("- %d\n", i) },
			decode: decodeAs[[]int],
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

			benchSelfValidation(b, fmt.Sprintf("%s_%d", shape.name, items), sb.String(), shape.decode, 0, "")
		}
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
		decode      func(opts ...niceyaml.DecodeOption) func(context.Context, *niceyaml.Node) error
		name        string
		open, close string
	}{
		{
			name:   "map_tree",
			open:   "{kids: {a: ",
			close:  "}}",
			decode: decodeAs[branch],
		},
		{
			name:   "struct_chain",
			open:   "{ports: {a: 80}, next: ",
			close:  "}",
			decode: decodeAs[structLink],
		},
		{
			name:   "list_chain",
			open:   "{ports: {a: 80}, next: [",
			close:  "]}",
			decode: decodeAs[listLink],
		},
	}

	for _, shape := range shapes {
		for _, depth := range []int{500, 1000, 2000} {
			yaml := strings.Repeat(shape.open, depth) + "{}" + strings.Repeat(shape.close, depth)

			benchSelfValidation(b, fmt.Sprintf("%s_%d", shape.name, depth), yaml, shape.decode, depth, "level")
		}
	}
}

// BenchmarkNode_Decode_SelfValidation_DeepMap decodes a tree of maps with
// and without self-validation. Only the leaf at the bottom validates
// itself, so before the walk enters each map it scans the values below
// the map for a validator. The scan reads each value once across the
// walk, so the time per level should stay close to flat as the tree
// deepens.
func BenchmarkNode_Decode_SelfValidation_DeepMap(b *testing.B) {
	for _, depth := range []int{250, 1000, 2000} {
		yaml := strings.Repeat("{kids: {a: ", depth) + "{leaf: 80}" + strings.Repeat("}}", depth)

		benchSelfValidation(b, fmt.Sprintf("depth_%d", depth), yaml, decodeAs[mapTree], depth, "level")
	}
}

// BenchmarkNode_Decode_SelfValidation_Promoted decodes a list of rows
// with and without self-validation. Each row embeds a struct and gets its
// Validate from that struct, so the walk validates the embedded struct
// rather than the row. The time per row should stay flat as the list
// grows.
func BenchmarkNode_Decode_SelfValidation_Promoted(b *testing.B) {
	for _, rows := range []int{4000, 16000} {
		yaml := yamltest.GenerateLines(rows, "- {name: n%[1]d, port: 80}")

		benchSelfValidation(b, fmt.Sprintf("rows_%d", rows), yaml, decodeAs[[]promotedRow], rows, "row")
	}
}

// benchSelfValidation runs a benchmark under name for each of
// [selfValidationModes]. Each benchmark decodes the first document of
// yaml through decode with the options of its mode. When units is
// positive, each also reports the time of one unit of work as
// ns/<unit>. A unit is one part of the document, such as a level or a
// row.
func benchSelfValidation(
	b *testing.B,
	name, yaml string,
	decode func(opts ...niceyaml.DecodeOption) func(context.Context, *niceyaml.Node) error,
	units int,
	unit string,
) {
	b.Helper()

	doc := yamltest.FirstDocument(b, yaml, yamltest.NoSizeLimits()...)

	for _, mode := range selfValidationModes {
		run := decode(mode.opts...)

		b.Run(name+"/"+mode.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				require.NoError(b, run(b.Context(), doc))
			}

			if units > 0 {
				yamltest.ReportTimePer(b, units, unit)
			}
		})
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

// mapTree is a node of a tree that holds its children in a map. Only its
// leaf validates itself.
type mapTree struct {
	Kids map[string]*mapTree `yaml:"kids"`
	Leaf *port               `yaml:"leaf"`
}

// promotedRow is a row that gets its Validate from the struct it embeds.
type promotedRow struct {
	Checked `yaml:",inline"`

	Name string `yaml:"name"`
}

// Checked validates the struct that embeds it.
type Checked struct {
	Port int `yaml:"port"`
}

func (*Checked) Validate() error { return nil }

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

// BenchmarkNode_Validate_EachBesideReferenceMerge validates a list whose
// items each merge a mapping of the document and a mapping of a
// reference document, as in `<<: [*big, *ref]`. The validator of each
// item fails at the key v that the item writes, so each failure binds
// through the merge sources. The lookups keep the keys of each merge
// source that they read. In the past_limit layout, the aliases to big
// take the document past the alias limit, so the bindings do not read
// the merge sources. The trusted layout turns the limit off, and the
// reference layout merges a reference mapping of 1000 keys alone. The
// time per item stays flat as the list grows in each layout. The metric
// retained-B is the heap that the first validation leaves live on a new
// document and its Source. The documents of each layout read one
// reference Source. It keeps the keys of its anchor that the validations
// before the count read, so the count does not hold them.
func BenchmarkNode_Validate_EachBesideReferenceMerge(b *testing.B) {
	keys := func(count int) string {
		parts := make([]string, count)
		for i := range parts {
			parts[i] = fmt.Sprintf("k%d: 1", i)
		}

		return "{" + strings.Join(parts, ", ") + "}"
	}

	item := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.NewError("v must be 2", niceyaml.AtPath(paths.Current().Child("v")))
	})
	v := niceyaml.Each(paths.MustParse("$.items[*]"), item)

	layouts := []struct {
		name, big, ref, merge string
		trusted               bool
	}{
		{name: "past_limit", big: keys(10000), ref: "{zz: 1}", merge: "[*big, *ref]"},
		{name: "trusted", big: keys(1000), ref: "{zz: 1}", merge: "[*big, *ref]", trusted: true},
		{name: "reference", big: "{}", ref: keys(1000), merge: "*ref"},
	}

	for _, layout := range layouts {
		opts := yamltest.NoSizeLimits(yamltest.References("ref: &ref " + layout.ref + "\n"))
		if layout.trusted {
			opts = append(opts, niceyaml.WithAliasLimit(false))
		}

		for _, items := range []int{250, 1000, 4000} {
			input := "big: &big " + layout.big + "\nitems:\n" +
				yamltest.GenerateLines(items, "  - {v: %d, <<: "+layout.merge+"}")
			newDoc := func() *niceyaml.Node { return yamltest.FirstDocument(b, input, opts...) }
			validate := func(doc *niceyaml.Node) {
				if doc.Validate(b.Context(), v) == nil {
					b.Fatal("got nil, want an error for each item")
				}
			}

			b.Run(fmt.Sprintf("%s_%d", layout.name, items), func(b *testing.B) {
				b.ReportAllocs()

				doc := newDoc()

				for b.Loop() {
					validate(doc)
				}

				yamltest.ReportTimePer(b, items, "item")
				reportRetained(b, newDoc, validate)
			})
		}
	}
}

// BenchmarkNode_Validate_WarnBesideReferenceMerge validates a mapping whose
// `<<` merge key brings in the keys of a reference anchor, with a validator
// that warns at each of those keys through [niceyaml.Node.Warn]. Each
// warning binds in a call of its own, which looks the key up through the
// anchor, and goes to the [niceyaml.Warnings] collector of the context.
// The reference Source keeps the keys of the anchor after the first
// lookup, so the lookups take time linear in the count of keys. The
// collector adds its own cost for each warning that it holds. The metric
// retained-B is the heap that the first validation leaves live on a new
// document and its Source. The documents of each size read one reference
// Source, which keeps the keys of its anchor from the validations before
// the count. So the count does not hold them.
func BenchmarkNode_Validate_WarnBesideReferenceMerge(b *testing.B) {
	for _, keys := range []int{1000, 2000, 4000} {
		ref := yamltest.References("u: &u\n" + yamltest.GenerateLines(keys, "  k%d: 1"))
		newDoc := func() *niceyaml.Node {
			return yamltest.FirstDocument(b, "x:\n  <<: *u\n", yamltest.NoSizeLimits(ref)...)
		}

		warns := make([]error, 0, keys)
		for i := range keys {
			at := niceyaml.AtPath(paths.Doc().Child("x", fmt.Sprintf("k%d", i)))
			warns = append(warns, niceyaml.NewWarning("old key", at))
		}

		v := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			for _, warn := range warns {
				n.Warn(ctx, warn)
			}

			return nil
		})

		validate := func(doc *niceyaml.Node) {
			warnings := niceyaml.NewWarnings()
			if doc.Validate(warnings.Context(b.Context()), v) != nil || warnings.Err() == nil {
				b.Fatal("got an error or no warnings, want a warning for each key")
			}
		}

		b.Run(fmt.Sprintf("keys_%d", keys), func(b *testing.B) {
			b.ReportAllocs()

			doc := newDoc()

			for b.Loop() {
				validate(doc)
			}

			yamltest.ReportTimePer(b, keys, "warning")
			reportRetained(b, newDoc, validate)
		})
	}
}
