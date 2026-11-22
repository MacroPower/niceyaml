package encoder_test

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/encoder"
)

func BenchmarkMarshal_comments(b *testing.B) {
	for _, keys := range []int{100, 1000, 10000} {
		input := make(map[string]int, keys)
		comments := make(yaml.CommentMap, keys)

		for i := range keys {
			key := fmt.Sprintf("key%05d", i)
			input[key] = i
			comments["$."+key] = []*yaml.Comment{yaml.LineComment(" line " + key)}
		}

		b.Run(fmt.Sprintf("keys_%d/none", keys), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := encoder.Marshal(b.Context(), input)
				require.NoError(b, err)
			}
		})

		b.Run(fmt.Sprintf("keys_%d/line", keys), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := encoder.Marshal(b.Context(), input, encoder.WithYAMLComments(comments))
				require.NoError(b, err)
			}
		})
	}
}
