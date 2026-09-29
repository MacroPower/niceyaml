package normalizer_test

import (
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml/normalizer"
)

func BenchmarkNormalizerNormalize(b *testing.B) {
	n := normalizer.New()

	inputs := []struct {
		name  string
		input string
	}{
		{"ascii_short", "hello"},
		{"ascii_long", strings.Repeat("hello world ", 100)},
		{"unicode_short", "Héllo Wörld"},
		{"unicode_long", strings.Repeat("Héllo Wörld Ñoño ", 100)},
		{"mixed", "Hello Héllo 日本語 Wörld"},
	}

	for _, in := range inputs {
		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(in.input)))

			for b.Loop() {
				_ = n.Normalize(in.input)
			}
		})
	}
}
