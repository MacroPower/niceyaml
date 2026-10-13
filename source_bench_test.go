package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// sourceBenchItem is the format of one entry of a sequence of items, which
// [yamltest.GenerateLines] numbers.
var sourceBenchItem = stringtest.JoinLF(
	"  - name: item_%[1]d",
	"    value: %[1]d",
)

// generateNestedYAML creates YAML nested to the given depth, where each
// level branches into itemsPerLevel keys.
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
				_ = niceyaml.NewSourceFromString(yaml, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0))
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
				_ = niceyaml.NewSourceFromString(yaml, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0))
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
		lines := niceyaml.NewSourceFromString(yaml, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0)).Lines()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				count := 0
				for range lines.Runes() {
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
		lines := niceyaml.NewSourceFromString(yaml, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0)).Lines()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				count := 0
				for range lines.All() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLen(b *testing.B) {
	yaml := yamltest.GenerateYAML(5000)
	lines := niceyaml.NewSourceFromString(yaml, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0)).Lines()

	b.ReportAllocs()

	for b.Loop() {
		_ = lines.Len()
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
		source := niceyaml.NewSourceFromString(yaml, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0))

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = source.Lines().Content()
			}
		})
	}
}

func BenchmarkSourceBind_ManyDocuments(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_100", 100},
		{"docs_1000", 1000},
	}

	for _, sz := range sizes {
		// Each document spans three lines, and the error joins one
		// finding per line, as a line-oriented lint reports them.
		source := niceyaml.NewSourceFromString(
			strings.Repeat(stringtest.LinesLF("---", "a: 1", "b: 2"), sz.docs),
			niceyaml.WithByteLimit(0),
			niceyaml.WithDepthLimit(0),
		)

		source.Documents()

		total := source.Lines().Len()
		findings := make([]error, 0, total)

		for i := range total {
			rng := yamltest.LineRange(i, 0, 1)
			findings = append(findings, niceyaml.NewError("finding", niceyaml.AtRange(rng)))
		}

		joined := errors.Join(findings...)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				require.Error(b, source.Bind(joined))
			}
		})
	}
}

func BenchmarkSource_File_ManyDocuments(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_1000", 1000},
		{"docs_8000", 8000},
	}

	for _, sz := range sizes {
		input := yamltest.GenerateLines(sz.docs, stringtest.JoinLF("---", "k%[1]d: v%[1]d"))

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))

			for b.Loop() {
				// File parses once per Source, so each iteration lexes a
				// new one outside the timer.
				b.StopTimer()

				source := niceyaml.NewSourceFromString(input, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0))
				source.Tokens()

				b.StartTimer()

				file, err := source.File()
				require.NoError(b, err)
				require.Len(b, file.Docs, sz.docs)
			}
		})
	}
}
