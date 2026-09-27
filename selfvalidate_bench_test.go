package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
)

// BenchmarkNode_Decode_SelfValidation decodes a list of small mappings, and
// a mapping that holds as many small mappings, with and without
// self-validation, which reads the keys of every map in the decoded value
// from the document. The time per item should stay flat as either grows.
func BenchmarkNode_Decode_SelfValidation(b *testing.B) {
	shapes := []struct {
		name   string
		header string
		item   func(i int) string
	}{
		{
			name:   "list",
			header: "items:\n",
			item:   func(int) string { return "  - {name: x, value: 1, tags: {a: 1}}\n" },
		},
		{
			name: "wide_map",
			item: func(i int) string { return fmt.Sprintf("k%d: {a: 1}\n", i) },
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

			doc, err := niceyaml.NewSourceFromString(yaml).Document()
			if err != nil {
				b.Fatal(err)
			}

			for _, mode := range modes {
				b.Run(fmt.Sprintf("%s_%d/%s", shape.name, items, mode.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(yaml)))

					for b.Loop() {
						_, err := doc.Decode[any](b.Context(), mode.opts...)
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
