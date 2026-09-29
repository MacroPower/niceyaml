package diff_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
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

			linesA, linesB := sourceA.Lines(), sourceB.Lines()

			for b.Loop() {
				_ = diff.Diff(linesA, linesB).Unified()
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

			linesA, linesB := sourceA.Lines(), sourceB.Lines()

			for b.Loop() {
				_ = diff.Diff(linesA, linesB).Unified()
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

			linesA, linesB := sourceA.Lines(), sourceB.Lines()

			for b.Loop() {
				_ = diff.Diff(linesA, linesB).Unified()
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

				linesA, linesB := sourceA.Lines(), sourceB.Lines()

				for b.Loop() {
					_ = diff.Diff(linesA, linesB).Hunks(ctx)
				}
			})
		}
	}
}

func BenchmarkSideBySideDiffSource(b *testing.B) {
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

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			linesA, linesB := sourceA.Lines(), sourceB.Lines()

			for b.Loop() {
				result := diff.Diff(linesA, linesB)
				_ = result.Before()
				_ = result.After()
			}
		})
	}
}

func BenchmarkFullDiffSource_WorstCase(b *testing.B) {
	// Worst case: both inputs hold the same lines in a different order.
	// Every line stays in the search, and the edits it walks through grow
	// with the input, so the time grows close to the square of its length.
	sizes := []int{100, 500, 1000}

	for _, size := range sizes {
		yamlA := yamltest.GenerateYAML(size)
		sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))

		reversed := slices.Collect(strings.Lines(yamlA))
		slices.Reverse(reversed)

		shuffled := slices.Collect(strings.Lines(yamlA))
		rng := rand.New(rand.NewPCG(1, 2))
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		orders := []struct {
			name  string
			lines []string
		}{
			{"reversed", reversed},
			{"shuffled", shuffled},
		}

		for _, order := range orders {
			yamlB := strings.Join(order.lines, "")
			sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

			b.Run(fmt.Sprintf("%s_%d", order.name, size), func(b *testing.B) {
				b.ReportAllocs()

				linesA, linesB := sourceA.Lines(), sourceB.Lines()

				for b.Loop() {
					_ = diff.Diff(linesA, linesB).Unified()
				}
			})
		}
	}
}

func BenchmarkFullDiffSource_NearIdentical(b *testing.B) {
	// A watched file that changes one line at a time diffs two
	// near-identical revisions. Two edits far apart leave almost the whole
	// file between the shared start and end. A changed line appears in only
	// one input, so the search leaves it out and finds nothing to walk. Two
	// lines that swap places stay in both inputs, so the search walks the
	// whole file with only a few edits.
	const size = 20000

	yamlA := yamltest.GenerateYAML(size)
	sourceA := niceyaml.NewSourceFromString(yamlA, niceyaml.WithName("a"))

	swapped := slices.Collect(strings.Lines(yamlA))
	swapped[1], swapped[size-2] = swapped[size-2], swapped[1]

	changes := map[string]string{
		"identical":        yamlA,
		"one_line_changed": strings.Replace(yamlA, "key_10000: value_10000\n", "key_10000: changed\n", 1),
		"two_far_edits": strings.Replace(
			strings.Replace(yamlA, "key_1: value_1\n", "key_1: changed\n", 1),
			"key_19998: value_19998\n", "key_19998: changed\n", 1,
		),
		"two_far_swapped": strings.Join(swapped, ""),
	}

	for name, yamlB := range changes {
		sourceB := niceyaml.NewSourceFromString(yamlB, niceyaml.WithName("b"))

		b.Run(fmt.Sprintf("%s_%d", name, size), func(b *testing.B) {
			b.ReportAllocs()

			linesA, linesB := sourceA.Lines(), sourceB.Lines()

			for b.Loop() {
				_ = diff.Diff(linesA, linesB).Unified()
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

			linesA, linesB := sourceA.Lines(), sourceB.Lines()

			for b.Loop() {
				_ = diff.Diff(linesA, linesB).Unified()
			}
		})
	}
}
