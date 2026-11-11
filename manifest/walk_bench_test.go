package manifest_test

import (
	"fmt"
	"path"
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

// benchTree returns the files of a tree of regular files with no link.
// Each directory down to depth holds fanOut subdirectories, and each
// directory at depth holds files files.
func benchTree(depth, fanOut, files int) map[string]string {
	out := map[string]string{}

	var fill func(dir string, level int)

	fill = func(dir string, level int) {
		if level == depth {
			for i := range files {
				out[path.Join(dir, fmt.Sprintf("f%d.yaml", i))] = hoursV1Doc
			}

			return
		}

		for i := range fanOut {
			fill(path.Join(dir, fmt.Sprintf("d%d", i)), level+1)
		}
	}

	fill(".", 0)

	return out
}

func BenchmarkPath_Walk(b *testing.B) {
	inputs := []struct {
		name  string
		files map[string]string
	}{
		{"flat_2000", benchTree(0, 0, 2000)},
		{"chain_20_500", benchTree(20, 1, 500)},
		{"balanced_4x3_20", benchTree(4, 3, 20)},
	}

	reg, err := manifest.NewRegistry(cafeOptions()...)
	if err != nil {
		b.Fatal(err)
	}

	for _, in := range inputs {
		dir := b.TempDir()
		yamltest.WriteFiles(b, dir, in.files)

		input := manifest.Path(dir, manifest.WithRecursive(true))

		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				chk, err := reg.Check(b.Context(), input, manifest.WithSetChecks(false))
				if err != nil {
					b.Fatal(err)
				}

				if n := len(chk.Set().Sources()); n != len(in.files) {
					b.Fatalf("got %d files, want %d", n, len(in.files))
				}
			}
		})
	}
}
