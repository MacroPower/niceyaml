package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// generateNestedYAML creates nested YAML content to test deeper structures.
func generateNestedYAML(depth, itemsPerLevel int) string {
	var sb strings.Builder

	var writeLevel func(level int)

	writeLevel = func(level int) {
		indent := strings.Repeat("  ", level)
		for i := range itemsPerLevel {
			if level < depth-1 {
				fmt.Fprintf(&sb, "%slevel%d_item%d:\n", indent, level, i)
				writeLevel(level + 1)
			} else {
				fmt.Fprintf(&sb, "%skey_%d: value_%d\n", indent, i, i)
			}
		}
	}

	sb.WriteString("root:\n")
	writeLevel(1)

	return sb.String()
}

func BenchmarkNewSourceFromString(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
		{"xlarge_50000", 50000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = niceyaml.NewSourceFromString(yaml)
			}
		})
	}
}

func BenchmarkNewSourceFromString_Nested(b *testing.B) {
	sizes := []struct {
		name          string
		depth         int
		itemsPerLevel int
	}{
		{"shallow_wide", 2, 100},
		{"deep_narrow", 10, 5},
		{"balanced", 5, 20},
	}

	for _, sz := range sizes {
		yaml := generateNestedYAML(sz.depth, sz.itemsPerLevel)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = niceyaml.NewSourceFromString(yaml)
			}
		})
	}
}

func BenchmarkSourceRunes(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				count := 0
				for range source.AllRunes() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLines(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				count := 0
				for range source.AllLines() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLen(b *testing.B) {
	yaml := yamltest.GenerateYAML(5000)
	source := niceyaml.NewSourceFromString(yaml)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = source.Len()
	}
}

func BenchmarkSourceContent(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = source.Lines().Content()
			}
		})
	}
}
