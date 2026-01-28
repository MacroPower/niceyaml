package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"jacobcolvin.com/niceyaml"
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
		yamlA := generateYAML(sz.lines)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))
		revA := niceyaml.NewRevision(sourceA)

		b.Run(sz.name+"/identical", func(b *testing.B) {
			yamlB := generateYAML(sz.lines)
			sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))
			revB := niceyaml.NewRevision(sourceB)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = niceyaml.Diff(revA, revB).Full()
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
			revB := niceyaml.NewRevision(sourceB)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = niceyaml.Diff(revA, revB).Full()
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
			revB := niceyaml.NewRevision(sourceB)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = niceyaml.Diff(revA, revB).Full()
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
		yamlA := generateYAML(sz.lines)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))
		revA := niceyaml.NewRevision(sourceA)

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
		revB := niceyaml.NewRevision(sourceB)

		for _, ctx := range contexts {
			b.Run(fmt.Sprintf("%s/context_%d", sz.name, ctx), func(b *testing.B) {
				b.ReportAllocs()

				for b.Loop() {
					_, _ = niceyaml.Diff(revA, revB).Hunks(ctx)
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
		revA := niceyaml.NewRevision(sourceA)
		revB := niceyaml.NewRevision(sourceB)

		b.Run(fmt.Sprintf("interleaved_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = niceyaml.Diff(revA, revB).Full()
			}
		})
	}
}

func BenchmarkFullDiffSource_InsertAtEnd(b *testing.B) {
	// Best case for LCS: append-only changes.
	sizes := []int{100, 500, 1000}

	for _, size := range sizes {
		yamlA := generateYAML(size)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))
		revA := niceyaml.NewRevision(sourceA)

		// Same content + 10% more at the end.
		var sb strings.Builder
		sb.WriteString(yamlA)

		for i := size; i < size+size/10; i++ {
			fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
		}

		yamlB := sb.String()
		sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))
		revB := niceyaml.NewRevision(sourceB)

		b.Run(fmt.Sprintf("append_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = niceyaml.Diff(revA, revB).Full()
			}
		})
	}
}

func BenchmarkRevisionSeek(b *testing.B) {
	// Build a chain of revisions.
	yaml := generateYAML(50)
	source := niceyaml.NewSourceFromString(yaml, niceyaml.WithName("v1"))
	rev := niceyaml.NewRevision(source)

	// Add 100 more revisions.
	for i := 2; i <= 100; i++ {
		s := niceyaml.NewSourceFromString(yaml, niceyaml.WithName(fmt.Sprintf("v%d", i)))
		rev = rev.Append(s)
	}

	seekDistances := []int{1, 10, 50, 99}

	for _, dist := range seekDistances {
		b.Run(fmt.Sprintf("seek_%d", dist), func(b *testing.B) {
			start := rev.Origin()

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = start.Seek(dist)
			}
		})
	}
}

func BenchmarkRevisionLen(b *testing.B) {
	yaml := generateYAML(50)
	source := niceyaml.NewSourceFromString(yaml, niceyaml.WithName("v1"))
	rev := niceyaml.NewRevision(source)

	// Add 100 more revisions.
	for i := 2; i <= 100; i++ {
		s := niceyaml.NewSourceFromString(yaml, niceyaml.WithName(fmt.Sprintf("v%d", i)))
		rev = rev.Append(s)
	}

	b.Run("from_origin", func(b *testing.B) {
		start := rev.Origin()

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_ = start.Len()
		}
	})

	b.Run("from_middle", func(b *testing.B) {
		middle := rev.At(50)

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_ = middle.Len()
		}
	})

	b.Run("from_tip", func(b *testing.B) {
		tip := rev.Tip()

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_ = tip.Len()
		}
	})
}

func BenchmarkRevisionNames(b *testing.B) {
	yaml := generateYAML(50)
	source := niceyaml.NewSourceFromString(yaml, niceyaml.WithName("v1"))
	rev := niceyaml.NewRevision(source)

	counts := []int{10, 50, 100}

	for _, count := range counts {
		r := rev
		for i := 2; i <= count; i++ {
			s := niceyaml.NewSourceFromString(yaml, niceyaml.WithName(fmt.Sprintf("v%d", i)))
			r = r.Append(s)
		}

		b.Run(fmt.Sprintf("%d_revisions", count), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = r.Names()
			}
		})
	}
}
