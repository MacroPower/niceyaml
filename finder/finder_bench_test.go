package finder_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/normalizer"
)

// benchSizes lists the input sizes the find benchmarks share.
var benchSizes = []struct {
	name  string
	lines int
}{
	{"small_50", 50},
	{"medium_500", 500},
	{"large_5000", 5000},
}

// benchFind measures a search of yaml through an index that f loads once,
// before the timer starts.
func benchFind(b *testing.B, f *finder.Finder, yaml, search string) {
	b.Helper()

	idx := f.Load(niceyaml.NewSourceFromString(yaml).Lines())

	b.ReportAllocs()
	b.SetBytes(int64(len(yaml)))

	for b.Loop() {
		_ = idx.Find(search)
	}
}

func BenchmarkFinderFind(b *testing.B) {
	searches := []struct {
		name   string
		search string
	}{
		// Appears rarely.
		{"few_matches", "key_0:"},
		// Appears on every line.
		{"many_matches", "value_"},
		// Does not exist.
		{"no_matches", "ZZZZZ_NOT_FOUND"},
	}

	for _, sz := range benchSizes {
		yaml := yamltest.GenerateYAML(sz.lines)

		for _, tc := range searches {
			b.Run(sz.name+"/"+tc.name, func(b *testing.B) {
				benchFind(b, finder.New(), yaml, tc.search)
			})
		}
	}
}

func BenchmarkFinderFind_WithNormalizer(b *testing.B) {
	for _, sz := range benchSizes {
		yaml := yamltest.GenerateYAML(sz.lines)

		b.Run(sz.name+"/without_normalizer", func(b *testing.B) {
			benchFind(b, finder.New(finder.WithNormalizer(nil)), yaml, "value_")
		})

		b.Run(sz.name+"/with_normalizer", func(b *testing.B) {
			benchFind(b, finder.New(finder.WithNormalizer(normalizer.New())), yaml, "value_")
		})
	}
}

func BenchmarkFinderFind_SearchLength(b *testing.B) {
	yaml := yamltest.GenerateYAML(1000)

	lengths := []int{1, 5, 10, 20, 50}

	for _, length := range lengths {
		search := strings.Repeat("x", length)

		b.Run(fmt.Sprintf("length_%d", length), func(b *testing.B) {
			benchFind(b, finder.New(), yaml, search)
		})
	}
}

func BenchmarkFinderFind_UnicodeContent(b *testing.B) {
	yaml := yamltest.GenerateLines(500, `key_%[1]d: "Héllo Wörld Ñoño %[1]d"`)

	b.Run("without_normalizer", func(b *testing.B) {
		benchFind(b, finder.New(finder.WithNormalizer(nil)), yaml, "Héllo")
	})

	b.Run("with_normalizer", func(b *testing.B) {
		// The normalizer converts "Héllo" to "hello".
		benchFind(b, finder.New(finder.WithNormalizer(normalizer.New())), yaml, "hello")
	})
}

func BenchmarkFinderCreate(b *testing.B) {
	yaml := yamltest.GenerateYAML(500)
	source := niceyaml.NewSourceFromString(yaml)

	b.Run("without_normalizer", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			f := finder.New(finder.WithNormalizer(nil))
			_ = f.Load(source.Lines())
		}
	})

	b.Run("with_normalizer", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			f := finder.New(
				finder.WithNormalizer(normalizer.New()),
			)
			_ = f.Load(source.Lines())
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

		b.Run(d.name, func(b *testing.B) {
			benchFind(b, finder.New(), yaml, "FINDME")
		})
	}
}
