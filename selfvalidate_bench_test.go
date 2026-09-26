package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
)

// BenchmarkNode_Decode_SelfValidation decodes a list of small mappings
// with and without self-validation, which reads the keys of every map in
// the decoded value from the document. The time per item should stay
// flat as the list grows.
func BenchmarkNode_Decode_SelfValidation(b *testing.B) {
	sizes := []struct {
		name  string
		items int
	}{
		{"items_1000", 1000},
		{"items_4000", 4000},
		{"items_16000", 16000},
	}

	modes := []struct {
		name string
		opts []niceyaml.DecodeOption
	}{
		{"self_validation", nil},
		{"no_self_validation", []niceyaml.DecodeOption{niceyaml.WithSelfValidation(false)}},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		sb.WriteString("items:\n")

		for range sz.items {
			sb.WriteString("  - {name: x, value: 1, tags: {a: 1}}\n")
		}

		yaml := sb.String()

		doc, err := niceyaml.NewSourceFromString(yaml).Document()
		if err != nil {
			b.Fatal(err)
		}

		for _, mode := range modes {
			b.Run(fmt.Sprintf("%s/%s", sz.name, mode.name), func(b *testing.B) {
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
