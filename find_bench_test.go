package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"jacobcolvin.com/niceyaml"
)

func BenchmarkFinderFind(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := generateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name+"/few_matches", func(b *testing.B) {
			// Create finder once (preprocesses source).
			finder := niceyaml.NewFinder()
			finder.Load(source)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				// Search for something that appears rarely.
				_ = finder.Find("key_0:")
			}
		})

		b.Run(sz.name+"/many_matches", func(b *testing.B) {
			// Create finder once (preprocesses source).
			finder := niceyaml.NewFinder()
			finder.Load(source)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				// Search for something that appears on every line.
				_ = finder.Find("value_")
			}
		})

		b.Run(sz.name+"/no_matches", func(b *testing.B) {
			// Create finder once (preprocesses source).
			finder := niceyaml.NewFinder()
			finder.Load(source)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				// Search for something that doesn't exist.
				_ = finder.Find("ZZZZZ_NOT_FOUND")
			}
		})
	}
}

func BenchmarkFinderFind_WithNormalizer(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := generateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name+"/without_normalizer", func(b *testing.B) {
			finder := niceyaml.NewFinder()
			finder.Load(source)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = finder.Find("value_")
			}
		})

		b.Run(sz.name+"/with_normalizer", func(b *testing.B) {
			finder := niceyaml.NewFinder(
				niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
			)
			finder.Load(source)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = finder.Find("value_")
			}
		})
	}
}

func BenchmarkFinderFind_SearchLength(b *testing.B) {
	yaml := generateYAML(1000)
	source := niceyaml.NewSourceFromString(yaml)
	finder := niceyaml.NewFinder()
	finder.Load(source)

	lengths := []int{1, 5, 10, 20, 50}

	for _, length := range lengths {
		search := strings.Repeat("x", length)

		b.Run(fmt.Sprintf("length_%d", length), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = finder.Find(search)
			}
		})
	}
}

func BenchmarkFinderFind_UnicodeContent(b *testing.B) {
	// Generate YAML with unicode content.
	var sb strings.Builder
	for i := range 500 {
		fmt.Fprintf(&sb, "key_%d: \"Héllo Wörld Ñoño %d\"\n", i, i)
	}

	yaml := sb.String()
	source := niceyaml.NewSourceFromString(yaml)

	b.Run("without_normalizer", func(b *testing.B) {
		finder := niceyaml.NewFinder()
		finder.Load(source)

		b.ReportAllocs()
		b.SetBytes(int64(len(yaml)))
		b.ResetTimer()

		for b.Loop() {
			_ = finder.Find("Héllo")
		}
	})

	b.Run("with_normalizer", func(b *testing.B) {
		// StandardNormalizer converts "Héllo" -> "hello".
		finder := niceyaml.NewFinder(
			niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
		)
		finder.Load(source)

		b.ReportAllocs()
		b.SetBytes(int64(len(yaml)))
		b.ResetTimer()

		for b.Loop() {
			_ = finder.Find("hello")
		}
	})
}

func BenchmarkStandardNormalizerNormalize(b *testing.B) {
	normalizer := niceyaml.NewStandardNormalizer()

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
				_ = normalizer.Normalize(in.input)
			}
		})
	}
}

func BenchmarkFinderCreate(b *testing.B) {
	yaml := generateYAML(500)
	source := niceyaml.NewSourceFromString(yaml)

	b.Run("without_normalizer", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			finder := niceyaml.NewFinder()
			finder.Load(source)
		}
	})

	b.Run("with_normalizer", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			finder := niceyaml.NewFinder(
				niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
			)
			finder.Load(source)
		}
	})
}

func BenchmarkFinderFind_MatchDensity(b *testing.B) {
	// Test with varying match density.
	densities := []struct {
		name        string
		matchEveryN int // Insert match pattern every N lines.
	}{
		{"very_sparse_every_100", 100},
		{"sparse_every_10", 10},
		{"medium_every_5", 5},
		{"dense_every_1", 1},
	}

	for _, d := range densities {
		var sb strings.Builder
		for i := range 1000 {
			if i%d.matchEveryN == 0 {
				fmt.Fprintf(&sb, "key_%d: FINDME_%d\n", i, i)
			} else {
				fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
			}
		}

		yaml := sb.String()
		source := niceyaml.NewSourceFromString(yaml)
		finder := niceyaml.NewFinder()
		finder.Load(source)

		b.Run(d.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = finder.Find("FINDME")
			}
		})
	}
}
