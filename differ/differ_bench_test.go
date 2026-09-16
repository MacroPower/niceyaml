package differ_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/differ"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/revision"
)

func BenchmarkFullDiffSource(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_2000", 2000},
	}

	for _, sz := range sizes {
		yamlA := yamltest.GenerateYAML(sz.lines)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))

		b.Run(sz.name+"/identical", func(b *testing.B) {
			yamlB := yamltest.GenerateYAML(sz.lines)
			sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = differ.Diff(sourceA, sourceB).Unified()
			}
		})

		b.Run(sz.name+"/all_changed", func(b *testing.B) {
			// Generate completely different content.
			var sb strings.Builder

			for i := range sz.lines {
				fmt.Fprintf(&sb, "different_key_%d: different_value_%d\n", i, i)
			}

			yamlB := sb.String()
			sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = differ.Diff(sourceA, sourceB).Unified()
			}
		})

		b.Run(sz.name+"/partial_changes", func(b *testing.B) {
			// Change 10% of lines.
			var sb strings.Builder

			for i := range sz.lines {
				if i%10 == 0 {
					fmt.Fprintf(&sb, "modified_key_%d: modified_value_%d\n", i, i)
				} else {
					fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
				}
			}

			yamlB := sb.String()
			sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = differ.Diff(sourceA, sourceB).Unified()
			}
		})
	}
}

func BenchmarkHunksDiffSource(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_2000", 2000},
	}

	contexts := []int{0, 3, 10}

	for _, sz := range sizes {
		yamlA := yamltest.GenerateYAML(sz.lines)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))

		// Create B with 10% changed lines.
		var sb strings.Builder

		for i := range sz.lines {
			if i%10 == 0 {
				fmt.Fprintf(&sb, "modified_key_%d: modified_value_%d\n", i, i)
			} else {
				fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
			}
		}

		yamlB := sb.String()
		sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

		for _, ctx := range contexts {
			b.Run(fmt.Sprintf("%s/context_%d", sz.name, ctx), func(b *testing.B) {
				b.ReportAllocs()

				for b.Loop() {
					_ = differ.Diff(sourceA, sourceB).Hunks(ctx)
				}
			})
		}
	}
}

func BenchmarkFullDiffSource_WorstCase(b *testing.B) {
	// Worst case: interleaved insertions/deletions that maximize LCS computation.
	sizes := []int{100, 500, 1000}

	for _, size := range sizes {
		// Before: even numbers.
		var sbA strings.Builder

		for i := 0; i < size; i += 2 {
			fmt.Fprintf(&sbA, "line_%d: value_%d\n", i, i)
		}

		yamlA := sbA.String()

		// After: odd numbers.
		var sbB strings.Builder

		for i := 1; i < size; i += 2 {
			fmt.Fprintf(&sbB, "line_%d: value_%d\n", i, i)
		}

		yamlB := sbB.String()

		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))
		sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

		b.Run(fmt.Sprintf("interleaved_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = differ.Diff(sourceA, sourceB).Unified()
			}
		})
	}
}

func BenchmarkFullDiffSource_InsertAtEnd(b *testing.B) {
	// Best case for LCS: append-only changes.
	sizes := []int{100, 500, 1000}

	for _, size := range sizes {
		yamlA := yamltest.GenerateYAML(size)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))

		// Same content + 10% more at the end.
		var sb strings.Builder

		sb.WriteString(yamlA)

		for i := size; i < size+size/10; i++ {
			fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
		}

		yamlB := sb.String()
		sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

		b.Run(fmt.Sprintf("append_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = differ.Diff(sourceA, sourceB).Unified()
			}
		})
	}
}

func BenchmarkRevisionsNames(b *testing.B) {
	yaml := yamltest.GenerateYAML(50)

	counts := []int{10, 50, 100}

	for _, count := range counts {
		revs := make(revision.History, 0, count)
		for i := 1; i <= count; i++ {
			revs = append(revs, niceyaml.NewSourceFromString(yaml, niceyaml.WithName(fmt.Sprintf("v%d", i))))
		}

		b.Run(fmt.Sprintf("%d_revisions", count), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = revs.Names()
			}
		})
	}
}
